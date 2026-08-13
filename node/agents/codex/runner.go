package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dengzii/weaveflow/core"
)

const (
	maxCodexEventBytes = 4 * 1024 * 1024
	processWaitDelay   = 5 * time.Second
)

var errCodexOutputLimit = errors.New("Codex output exceeded configured limit")

type RunRequest struct {
	ModelID string
	Prompt  string
	OnChunk func(Chunk) error
}

type RunResult struct {
	ModelID   string            `json:"model_id"`
	ThreadID  string            `json:"thread_id,omitempty"`
	Output    string            `json:"output,omitempty"`
	Usage     Usage             `json:"usage"`
	Events    []json.RawMessage `json:"events,omitempty"`
	Stderr    string            `json:"stderr,omitempty"`
	ExitCode  int               `json:"exit_code"`
	Duration  time.Duration     `json:"duration"`
	Truncated bool              `json:"truncated,omitempty"`
}

type Runner interface {
	Run(context.Context, RunRequest) (RunResult, error)
}

type ProcessRunner struct {
	config         resolvedCodexRunnerConfig
	semaphore      chan struct{}
	workspaceMu    sync.Mutex
	workspaceLock  map[string]chan struct{}
	readyMu        sync.Mutex
	executable     string
	readyErr       error
	checkReadiness bool
	ready          bool
}

type resolvedCodexRun struct {
	resolvedCodexRunConfig
}

type outputResult struct {
	parser    *eventParser
	err       error
	truncated bool
}

type stderrResult struct {
	text      string
	err       error
	truncated bool
}

func NewProcessRunner(config RunnerConfig) (*ProcessRunner, error) {
	return newProcessRunner(config, true)
}

func newProcessRunner(config RunnerConfig, checkReadiness bool) (*ProcessRunner, error) {
	resolved, err := resolveCodexRunnerConfig(config)
	if err != nil {
		return nil, err
	}
	return &ProcessRunner{
		config:         resolved,
		semaphore:      make(chan struct{}, resolved.MaxConcurrency),
		workspaceLock:  map[string]chan struct{}{},
		checkReadiness: checkReadiness,
	}, nil
}

func (runner *ProcessRunner) ensureReady() (string, error) {
	if runner == nil {
		return "", fmt.Errorf("Codex runner is not configured")
	}
	runner.readyMu.Lock()
	defer runner.readyMu.Unlock()
	if runner.ready {
		return runner.executable, runner.readyErr
	}
	runner.ready = true
	runner.executable, runner.readyErr = resolveCodexExecutable(runner.config.Executable)
	if runner.readyErr == nil && runner.checkReadiness {
		runner.readyErr = validateCodexCapabilities(runner.executable)
	}
	return runner.executable, runner.readyErr
}

func validateCodexCapabilities(executable string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rootHelp, err := help(ctx, executable, "--help")
	if err != nil {
		return err
	}
	execHelp, err := help(ctx, executable, "exec", "--help")
	if err != nil {
		return err
	}
	return validateCodexHelp(rootHelp, execHelp)
}

func validateCodexHelp(rootHelp, execHelp string) error {
	for _, option := range []string{"--ask-for-approval", "--config"} {
		if !strings.Contains(rootHelp, option) {
			return fmt.Errorf("top-level %s is not supported", option)
		}
	}
	for _, option := range []string{"--json", "--ephemeral", "--ignore-user-config", "--ignore-rules", "--strict-config", "--sandbox", "--cd", "--model"} {
		if !strings.Contains(execHelp, option) {
			return fmt.Errorf("Codex exec option %s is not supported", option)
		}
	}
	return nil
}

func help(ctx context.Context, executable string, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, executable, arguments...)
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("run %s: %w", strings.Join(arguments, " "), err)
	}
	if len(output) > 1024*1024 {
		return "", fmt.Errorf("run %s: help output exceeds limit", strings.Join(arguments, " "))
	}
	return string(output), nil
}

func (runner *ProcessRunner) Run(ctx context.Context, request RunRequest) (RunResult, error) {
	if runner == nil {
		return RunResult{}, fmt.Errorf("Codex runner is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	modelID := strings.TrimSpace(request.ModelID)
	if strings.TrimSpace(request.Prompt) == "" {
		return RunResult{ModelID: modelID, ExitCode: -1}, fmt.Errorf("Codex prompt is empty")
	}
	modelConfig, ok := core.ModelConfigByIDFromContext(ctx, modelID)
	if !ok {
		return RunResult{ModelID: modelID, ExitCode: -1}, fmt.Errorf("Codex model %q is not configured in Graph Settings", effectiveCodexModelID(modelID))
	}
	modelID = strings.TrimSpace(modelConfig.ID)
	if modelID == "" {
		modelID = core.DefaultModelID
	}
	if err := validateCodexModelConfig(modelID, modelConfig); err != nil {
		return RunResult{ModelID: modelID, ExitCode: -1}, err
	}
	baseURL, err := normalizeCodexBaseURL(modelConfig.BaseURL)
	if err != nil {
		return RunResult{ModelID: modelID, ExitCode: -1}, fmt.Errorf("Codex model %q base_url: %w", modelID, err)
	}
	if strings.ContainsAny(modelConfig.APIKey, "\x00\r\n") {
		return RunResult{ModelID: modelID, ExitCode: -1}, fmt.Errorf("Codex model %q api_key contains an invalid control character", modelID)
	}
	workspacePath, err := resolveCodexWorkspace(ctx, runner.config)
	if err != nil {
		return RunResult{ModelID: modelID, ExitCode: -1}, err
	}
	environment, secretValues, err := environment(ctx, modelConfig.APIKey)
	if err != nil {
		return RunResult{ModelID: modelID, ExitCode: -1}, fmt.Errorf("Codex environment: %w", err)
	}
	executable, err := runner.ensureReady()
	if err != nil {
		return RunResult{ModelID: modelID, ExitCode: -1}, fmt.Errorf("Codex runtime check: %w", err)
	}
	runConfig := resolvedCodexRun{
		resolvedCodexRunConfig: resolvedCodexRunConfig{
			resolvedCodexRunnerConfig: runner.config,
			executablePath:            executable,
			workspacePath:             workspacePath,
			modelID:                   modelID,
			model:                     strings.TrimSpace(modelConfig.Model),
			baseURL:                   baseURL,
			environment:               environment,
			secretValues:              secretValues,
		},
	}

	if err := acquire(ctx, runner.semaphore); err != nil {
		return RunResult{ModelID: modelID, ExitCode: -1}, err
	}
	defer release(runner.semaphore)

	if runner.config.Sandbox == SandboxWorkspaceWrite {
		workspaceLock := runner.lockForWorkspace(workspacePath)
		if err := acquire(ctx, workspaceLock); err != nil {
			return RunResult{ModelID: modelID, ExitCode: -1}, err
		}
		defer release(workspaceLock)
	}
	return runCodexProcess(ctx, runConfig, request)
}

func effectiveCodexModelID(modelID string) string {
	if modelID = strings.TrimSpace(modelID); modelID != "" {
		return modelID
	}
	return core.DefaultModelID
}

func (runner *ProcessRunner) lockForWorkspace(workspace string) chan struct{} {
	runner.workspaceMu.Lock()
	defer runner.workspaceMu.Unlock()
	lock := runner.workspaceLock[workspace]
	if lock == nil {
		lock = make(chan struct{}, 1)
		runner.workspaceLock[workspace] = lock
	}
	return lock
}

func acquire(ctx context.Context, semaphore chan struct{}) error {
	select {
	case semaphore <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func release(semaphore chan struct{}) {
	<-semaphore
}

func runCodexProcess(ctx context.Context, config resolvedCodexRun, request RunRequest) (RunResult, error) {
	startedAt := time.Now()
	result := RunResult{ModelID: config.modelID, ExitCode: -1}
	runCtx, cancelRun := context.WithTimeout(ctx, time.Duration(config.TimeoutSeconds)*time.Second)
	defer cancelRun()

	command := exec.CommandContext(runCtx, config.executablePath, arguments(config)...)
	command.Dir = config.workspacePath
	command.Env = append([]string(nil), config.environment...)
	command.Stdin = strings.NewReader(request.Prompt)
	command.WaitDelay = processWaitDelay

	stdout, err := command.StdoutPipe()
	if err != nil {
		return result, fmt.Errorf("open Codex stdout: %w", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return result, fmt.Errorf("open Codex stderr: %w", err)
	}
	processTree, err := newProcessTree(command)
	if err != nil {
		return result, fmt.Errorf("prepare Codex process tree: %w", err)
	}
	defer func() { _ = processTree.Close() }()
	command.Cancel = processTree.Terminate

	if err := command.Start(); err != nil {
		return result, fmt.Errorf("start Codex: %w", err)
	}
	if err := processTree.Attach(command.Process); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return result, fmt.Errorf("attach Codex process tree: %w", err)
	}

	stdoutResult := make(chan outputResult, 1)
	stderrResults := make(chan stderrResult, 1)
	go func() {
		read := readCodexOutput(stdout, config.modelID, config.MaxStdoutBytes, request.OnChunk)
		if read.err != nil {
			cancelRun()
		}
		stdoutResult <- read
	}()
	go func() {
		read := readCodexStderr(stderr, config.MaxStderrBytes)
		if read.err != nil {
			cancelRun()
		}
		stderrResults <- read
	}()

	outputRead := <-stdoutResult
	stderrRead := <-stderrResults
	waitErr := command.Wait()
	result.Duration = time.Since(startedAt)
	if command.ProcessState != nil {
		result.ExitCode = command.ProcessState.ExitCode()
	}
	redactor := newSecretRedactor(config.secretValues)
	result.Stderr = redactor.text(stderrRead.text)
	result.Truncated = outputRead.truncated || stderrRead.truncated
	if outputRead.parser != nil {
		result.ThreadID = outputRead.parser.threadID
		result.Output = redactor.text(outputRead.parser.output)
		result.Usage = outputRead.parser.usage
		result.Events = redactCodexEvents(outputRead.parser.events, redactor)
	}

	if outputRead.err != nil {
		return result, redactedCodexError(redactor, outputRead.err)
	}
	if stderrRead.err != nil {
		return result, stderrRead.err
	}
	if runCtx.Err() != nil {
		return result, runCtx.Err()
	}
	if outputRead.parser != nil && outputRead.parser.failed != nil {
		return result, redactedCodexError(redactor, outputRead.parser.failed)
	}
	if waitErr != nil {
		if result.Stderr != "" {
			return result, fmt.Errorf("Codex exited with code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
		}
		return result, fmt.Errorf("Codex exited with code %d: %w", result.ExitCode, waitErr)
	}
	if outputRead.parser == nil || !outputRead.parser.completed {
		if outputRead.parser != nil && outputRead.parser.diagnostic != "" {
			return result, fmt.Errorf("Codex completed without turn.completed after error: %s", redactor.text(outputRead.parser.diagnostic))
		}
		return result, fmt.Errorf("Codex completed without turn.completed")
	}
	if closeErr := processTree.Close(); closeErr != nil {
		return result, fmt.Errorf("close Codex process tree: %w", closeErr)
	}
	if strings.TrimSpace(result.Output) == "" {
		return result, fmt.Errorf("Codex completed without an agent message")
	}
	return result, nil
}

func arguments(config resolvedCodexRun) []string {
	arguments := []string{"--ask-for-approval", "never"}
	if config.baseURL != "" {
		arguments = append(arguments, "-c", "openai_base_url="+strconv.Quote(config.baseURL))
	}
	arguments = append(arguments,
		"exec",
		"--json",
		"--ephemeral",
		"--ignore-user-config",
		"--ignore-rules",
		"--strict-config",
		"--sandbox", config.sandbox(),
		"--cd", config.workspacePath,
	)
	if config.model != "" {
		arguments = append(arguments, "--model", config.model)
	}
	return append(arguments, "-")
}

func (config resolvedCodexRun) sandbox() string {
	if config.Sandbox == SandboxWorkspaceWrite {
		return "workspace-write"
	}
	return "read-only"
}

func readCodexOutput(reader io.Reader, modelID string, maxBytes int64, onChunk func(Chunk) error) outputResult {
	parser := newCodexEventParser(modelID, onChunk)
	scanner := bufio.NewScanner(reader)
	maximumLineBytes := int64(maxCodexEventBytes)
	if maxBytes < maximumLineBytes {
		maximumLineBytes = maxBytes
	}
	if maximumLineBytes < 1 {
		maximumLineBytes = 1
	}
	scanner.Buffer(make([]byte, 64*1024), int(maximumLineBytes))
	var total int64
	for scanner.Scan() {
		line := scanner.Bytes()
		total += int64(len(line)) + 1
		if total > maxBytes {
			return outputResult{parser: parser, err: errCodexOutputLimit, truncated: true}
		}
		if err := parser.parse(line); err != nil {
			return outputResult{parser: parser, err: err}
		}
	}
	if err := scanner.Err(); err != nil {
		if strings.Contains(err.Error(), "token too long") {
			return outputResult{parser: parser, err: errCodexOutputLimit, truncated: true}
		}
		return outputResult{parser: parser, err: fmt.Errorf("read Codex JSONL: %w", err)}
	}
	return outputResult{parser: parser}
}

func redactedCodexError(redactor secretRedactor, err error) error {
	if err == nil {
		return nil
	}
	return errors.New(redactor.text(err.Error()))
}

func readCodexStderr(reader io.Reader, maxBytes int64) stderrResult {
	limited := &io.LimitedReader{R: reader, N: maxBytes + 1}
	data, err := io.ReadAll(limited)
	if err != nil {
		return stderrResult{err: fmt.Errorf("read Codex stderr: %w", err)}
	}
	if int64(len(data)) > maxBytes {
		return stderrResult{text: string(data[:maxBytes]), err: errCodexOutputLimit, truncated: true}
	}
	return stderrResult{text: string(data)}
}

func redactCodexEvents(events []json.RawMessage, redactor secretRedactor) []json.RawMessage {
	if len(events) == 0 {
		return nil
	}
	redacted := make([]json.RawMessage, 0, len(events))
	for _, event := range events {
		redacted = append(redacted, json.RawMessage(redactor.text(string(event))))
	}
	return redacted
}
