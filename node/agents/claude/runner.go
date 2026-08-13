package claude

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
)

const (
	maxClaudeEventBytes = 4 * 1024 * 1024
	processWaitDelay    = 5 * time.Second
)

var errClaudeOutputLimit = errors.New("Claude output exceeded configured limit")

type RunRequest struct {
	Prompt  string
	OnChunk func(Chunk) error
}

type RunResult struct {
	Model     string            `json:"model,omitempty"`
	SessionID string            `json:"session_id,omitempty"`
	Output    string            `json:"output,omitempty"`
	Usage     Usage             `json:"usage"`
	CostUSD   float64           `json:"cost_usd,omitempty"`
	NumTurns  int               `json:"num_turns,omitempty"`
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
	config         resolvedClaudeRunnerConfig
	semaphore      chan struct{}
	workspaceMu    sync.Mutex
	workspaceLock  map[string]chan struct{}
	readyMu        sync.Mutex
	executable     string
	readyErr       error
	checkReadiness bool
	ready          bool
}

type resolvedClaudeRun struct {
	resolvedClaudeRunConfig
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
	resolved, err := resolveClaudeRunnerConfig(config)
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
		return "", fmt.Errorf("Claude runner is not configured")
	}
	runner.readyMu.Lock()
	defer runner.readyMu.Unlock()
	if runner.ready {
		return runner.executable, runner.readyErr
	}
	runner.ready = true
	runner.executable, runner.readyErr = resolveClaudeExecutable(runner.config.Executable)
	if runner.readyErr == nil && runner.checkReadiness {
		runner.readyErr = validateClaudeCapabilities(runner.executable)
	}
	return runner.executable, runner.readyErr
}

func validateClaudeCapabilities(executable string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "--help")
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("run --help: %w", err)
	}
	if len(output) > 1024*1024 {
		return fmt.Errorf("run --help: help output exceeds limit")
	}
	return validateClaudeHelp(string(output))
}

func validateClaudeHelp(help string) error {
	for _, option := range []string{
		"--bare",
		"--print",
		"--output-format",
		"--verbose",
		"--include-partial-messages",
		"--permission-mode",
		"--tools",
		"--allowedTools",
		"--disallowedTools",
		"--max-budget-usd",
		"--no-session-persistence",
		"--strict-mcp-config",
		"--disable-slash-commands",
		"--model",
		"--settings",
	} {
		if !strings.Contains(help, option) {
			return fmt.Errorf("Claude option %s is not supported", option)
		}
	}
	return nil
}

func (runner *ProcessRunner) Run(ctx context.Context, request RunRequest) (RunResult, error) {
	if runner == nil {
		return RunResult{}, fmt.Errorf("Claude runner is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(request.Prompt) == "" {
		return RunResult{ExitCode: -1}, fmt.Errorf("Claude prompt is empty")
	}
	workspacePath, err := resolveClaudeWorkspace(ctx, runner.config)
	if err != nil {
		return RunResult{ExitCode: -1}, err
	}
	environment, secretValues, err := environment(runner.config)
	if err != nil {
		return RunResult{ExitCode: -1}, fmt.Errorf("Claude environment: %w", err)
	}
	executable, err := runner.ensureReady()
	if err != nil {
		return RunResult{ExitCode: -1}, fmt.Errorf("Claude runtime check: %w", err)
	}
	runConfig := resolvedClaudeRun{
		resolvedClaudeRunConfig: resolvedClaudeRunConfig{
			resolvedClaudeRunnerConfig: runner.config,
			executablePath:             executable,
			workspacePath:              workspacePath,
			environment:                environment,
			secretValues:               secretValues,
		},
	}

	if err := acquire(ctx, runner.semaphore); err != nil {
		return RunResult{ExitCode: -1}, err
	}
	defer release(runner.semaphore)

	if runner.config.Access == WorkspaceAccessWrite {
		workspaceLock := runner.lockForWorkspace(workspacePath)
		if err := acquire(ctx, workspaceLock); err != nil {
			return RunResult{ExitCode: -1}, err
		}
		defer release(workspaceLock)
	}
	return runClaudeProcess(ctx, runConfig, request)
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

func runClaudeProcess(ctx context.Context, config resolvedClaudeRun, request RunRequest) (RunResult, error) {
	startedAt := time.Now()
	result := RunResult{Model: config.Model, ExitCode: -1}
	redactor := newSecretRedactor(config.secretValues)
	runCtx, cancelRun := context.WithTimeout(ctx, time.Duration(config.TimeoutSeconds)*time.Second)
	defer cancelRun()

	command := exec.CommandContext(runCtx, config.executablePath, arguments(config)...)
	command.Dir = config.workspacePath
	command.Env = append([]string(nil), config.environment...)
	command.Stdin = strings.NewReader(request.Prompt)
	command.WaitDelay = processWaitDelay

	stdout, err := command.StdoutPipe()
	if err != nil {
		return result, fmt.Errorf("open Claude stdout: %w", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return result, fmt.Errorf("open Claude stderr: %w", err)
	}
	processTree, err := newProcessTree(command)
	if err != nil {
		return result, fmt.Errorf("prepare Claude process tree: %w", err)
	}
	defer func() { _ = processTree.Close() }()
	command.Cancel = processTree.Terminate

	if err := command.Start(); err != nil {
		return result, fmt.Errorf("start Claude: %w", err)
	}
	if err := processTree.Attach(command.Process); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return result, fmt.Errorf("attach Claude process tree: %w", err)
	}

	stdoutResult := make(chan outputResult, 1)
	stderrResults := make(chan stderrResult, 1)
	onChunk := request.OnChunk
	if onChunk != nil {
		onChunk = func(chunk Chunk) error {
			chunk.Text = redactor.text(chunk.Text)
			return request.OnChunk(chunk)
		}
	}
	go func() {
		read := readClaudeOutput(stdout, config.MaxStdoutBytes, onChunk)
		if read.err != nil {
			cancelRun()
		}
		stdoutResult <- read
	}()
	go func() {
		read := readClaudeStderr(stderr, config.MaxStderrBytes)
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
	result.Stderr = redactor.text(stderrRead.text)
	result.Truncated = outputRead.truncated || stderrRead.truncated
	if outputRead.parser != nil {
		if outputRead.parser.model != "" {
			result.Model = outputRead.parser.model
		}
		result.SessionID = outputRead.parser.sessionID
		result.Output = redactor.text(outputRead.parser.output)
		result.Usage = outputRead.parser.usage
		result.CostUSD = outputRead.parser.costUSD
		result.NumTurns = outputRead.parser.numTurns
		result.Events = redactClaudeEvents(outputRead.parser.events, redactor)
	}

	if outputRead.err != nil {
		return result, redactedClaudeError(redactor, outputRead.err)
	}
	if stderrRead.err != nil {
		return result, stderrRead.err
	}
	if runCtx.Err() != nil {
		return result, runCtx.Err()
	}
	if outputRead.parser != nil && outputRead.parser.failed != nil {
		return result, redactedClaudeError(redactor, outputRead.parser.failed)
	}
	if waitErr != nil {
		if result.Stderr != "" {
			return result, fmt.Errorf("Claude exited with code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
		}
		return result, fmt.Errorf("Claude exited with code %d: %w", result.ExitCode, waitErr)
	}
	if outputRead.parser == nil || !outputRead.parser.completed {
		return result, fmt.Errorf("Claude completed without a successful result event")
	}
	if closeErr := processTree.Close(); closeErr != nil {
		return result, fmt.Errorf("close Claude process tree: %w", closeErr)
	}
	if strings.TrimSpace(result.Output) == "" {
		return result, fmt.Errorf("Claude completed without a result")
	}
	return result, nil
}

func arguments(config resolvedClaudeRun) []string {
	arguments := []string{
		"--bare",
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--permission-mode", "dontAsk",
		"--strict-mcp-config",
		"--disable-slash-commands",
		"--no-session-persistence",
		"--tools", strings.Join(config.Tools, ","),
	}
	if len(config.AllowedTools) > 0 {
		arguments = append(arguments, "--allowedTools", strings.Join(config.AllowedTools, ","))
	}
	if len(config.DisallowedTools) > 0 {
		arguments = append(arguments, "--disallowedTools", strings.Join(config.DisallowedTools, ","))
	}
	if config.MaxBudgetUSD > 0 {
		arguments = append(arguments, "--max-budget-usd", strconv.FormatFloat(config.MaxBudgetUSD, 'f', -1, 64))
	}
	if config.Model != "" {
		arguments = append(arguments, "--model", config.Model)
	}
	if config.Access == WorkspaceAccessWrite {
		arguments = append(arguments, "--settings", `{"sandbox":{"enabled":true,"autoAllowBashIfSandboxed":false}}`)
	}
	return arguments
}

func readClaudeOutput(reader io.Reader, maxBytes int64, onChunk func(Chunk) error) outputResult {
	parser := newClaudeEventParser(onChunk)
	scanner := bufio.NewScanner(reader)
	maximumLineBytes := int64(maxClaudeEventBytes)
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
			return outputResult{parser: parser, err: errClaudeOutputLimit, truncated: true}
		}
		if err := parser.parse(line); err != nil {
			return outputResult{parser: parser, err: err}
		}
	}
	if err := scanner.Err(); err != nil {
		if strings.Contains(err.Error(), "token too long") {
			return outputResult{parser: parser, err: errClaudeOutputLimit, truncated: true}
		}
		return outputResult{parser: parser, err: fmt.Errorf("read Claude stream-json: %w", err)}
	}
	return outputResult{parser: parser}
}

func redactedClaudeError(redactor secretRedactor, err error) error {
	if err == nil {
		return nil
	}
	return errors.New(redactor.text(err.Error()))
}

func readClaudeStderr(reader io.Reader, maxBytes int64) stderrResult {
	limited := &io.LimitedReader{R: reader, N: maxBytes + 1}
	data, err := io.ReadAll(limited)
	if err != nil {
		return stderrResult{err: fmt.Errorf("read Claude stderr: %w", err)}
	}
	if int64(len(data)) > maxBytes {
		return stderrResult{text: string(data[:maxBytes]), err: errClaudeOutputLimit, truncated: true}
	}
	return stderrResult{text: string(data)}
}

func redactClaudeEvents(events []json.RawMessage, redactor secretRedactor) []json.RawMessage {
	if len(events) == 0 {
		return nil
	}
	redacted := make([]json.RawMessage, 0, len(events))
	for _, event := range events {
		redacted = append(redacted, json.RawMessage(redactor.text(string(event))))
	}
	return redacted
}
