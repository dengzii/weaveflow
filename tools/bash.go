package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dengzii/weaveflow/core"

	"github.com/dengzii/weaveflow/llms"
)

const (
	bashToolTimeoutEnv   = "WEAVEFLOW_BASH_TIMEOUT"
	bashToolAllowListEnv = "WEAVEFLOW_BASH_ALLOWLIST"
	defaultBashTimeout   = 2 * time.Minute
	maxBashTimeout       = 10 * time.Minute
	maxOutputSize        = 64 * 1024
	defaultShell         = "/bin/sh"
	windowsDefaultShell  = "cmd.exe"
)

type bashRequest struct {
	Command                   string `json:"command"`
	Timeout                   int    `json:"timeout,omitempty"`
	Description               string `json:"description"`
	RunInBackground           bool   `json:"run_in_background,omitempty"`
	DangerouslyDisableSandbox bool   `json:"dangerouslyDisableSandbox,omitempty"`
	Shell                     string `json:"shell,omitempty"`
}

type bashResponse struct {
	Command    string `json:"command"`
	Shell      string `json:"shell,omitempty"`
	ExitCode   int    `json:"exit_code"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	Truncated  bool   `json:"truncated,omitempty"`
	TimedOut   bool   `json:"timed_out,omitempty"`
	WorkingDir string `json:"working_dir,omitempty"`
}

type shellSpec struct {
	Name string
	Path string
	Args []string
}

func NewBash() Tool {
	return Tool{
		Function: &llms.FunctionDefinition{
			Name:        "bash",
			Description: "Execute a shell command and return the output. Commands run in a sandboxed workspace directory. Use with caution as it can modify files.",
			OutputSchema: objectOutputSchema(map[string]any{
				"command":     map[string]any{"type": "string"},
				"shell":       map[string]any{"type": "string"},
				"exit_code":   map[string]any{"type": "integer"},
				"stdout":      map[string]any{"type": "string"},
				"stderr":      map[string]any{"type": "string"},
				"truncated":   map[string]any{"type": "boolean"},
				"timed_out":   map[string]any{"type": "boolean"},
				"working_dir": map[string]any{"type": "string"},
			}, "command", "exit_code", "stdout", "stderr"),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "The command to execute",
					},
					"timeout": map[string]any{
						"type":        "number",
						"description": "Optional timeout in milliseconds (max 600000)",
					},
					"description": map[string]any{
						"type":        "string",
						"minLength":   1,
						"description": "Clear, concise description of what this command does in active voice.",
					},
					"run_in_background": map[string]any{
						"type":        "boolean",
						"description": "Set to true to run this command in the background.",
					},
					"dangerouslyDisableSandbox": map[string]any{
						"type":        "boolean",
						"description": "Set this to true to dangerously override sandbox mode and run commands without sandboxing.",
					},
					"shell": map[string]any{
						"type":        "string",
						"enum":        []string{"auto", "bash", "pwsh", "cmd", "git_bash", "mingw"},
						"description": "Optional shell runtime. auto uses Git Bash when available on Windows, then pwsh/cmd; elsewhere it uses bash/sh.",
					},
				},
				"required":             []string{"command", "description"},
				"additionalProperties": false,
			},
		},
		Handler:     bashTool,
		Permissions: []string{"process.execute"},
		Approval:    core.ToolApprovalRequired,
	}
}

func bashTool(ctx context.Context, call llms.ToolCall) (llms.ToolResult, error) {
	var req bashRequest
	if err := decodeToolArguments(call, &req); err != nil {
		return llms.ToolResult{}, fmt.Errorf("bash input: %w", err)
	}

	command := strings.TrimSpace(req.Command)
	if command == "" {
		return llms.ToolResult{}, errors.New("command is required")
	}
	req.Description = strings.TrimSpace(req.Description)
	if req.Description == "" {
		return llms.ToolResult{}, errors.New("description is required")
	}

	if err := validateBashCommand(ctx, command); err != nil {
		return llms.ToolResult{}, err
	}
	if req.RunInBackground {
		return llms.ToolResult{}, errors.New("run_in_background is not supported by this Bash tool")
	}

	timeout, err := normalizeBashTimeout(ctx, req.Timeout)
	if err != nil {
		return llms.ToolResult{}, err
	}
	workingDir, err := toolWorkspaceDir(ctx)
	if err != nil {
		return llms.ToolResult{}, err
	}

	result, err := executeBashCommand(ctx, command, workingDir, timeout, req.Shell)
	if err != nil {
		return llms.ToolResult{}, err
	}

	return structuredToolResultWithContent(call, *result, formatBashResponse(result)), nil
}

func executeBashCommand(ctx context.Context, command, workingDir string, timeout time.Duration, requestedShell string) (*bashResponse, error) {
	shell, err := resolveShell(ctx, requestedShell)
	if err != nil {
		return nil, err
	}
	environment, err := commandEnvironment(ctx)
	if err != nil {
		return nil, err
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := append([]string{}, shell.Args...)
	args = append(args, command)
	cmd := exec.CommandContext(execCtx, shell.Path, args...)
	cmd.Dir = workingDir
	cmd.Env = environment

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()

	exitCode := 0
	timedOut := false

	if err != nil {
		var exitErr *exec.ExitError
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			timedOut = true
			exitCode = -1
		} else if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("failed to execute command: %w", err)
		}
	}

	stdoutStr := stdout.String()
	stderrStr := stderr.String()
	truncated := false

	if len(stdoutStr) > maxOutputSize {
		stdoutStr = stdoutStr[:maxOutputSize]
		truncated = true
	}
	if len(stderrStr) > maxOutputSize {
		stderrStr = stderrStr[:maxOutputSize]
		truncated = true
	}

	return &bashResponse{
		Command:    command,
		Shell:      shell.Name,
		ExitCode:   exitCode,
		Stdout:     stdoutStr,
		Stderr:     stderrStr,
		Truncated:  truncated,
		TimedOut:   timedOut,
		WorkingDir: workingDir,
	}, nil
}

func formatBashResponse(resp *bashResponse) string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "exit_code: %d\n", resp.ExitCode)
	if resp.Shell != "" {
		_, _ = fmt.Fprintf(&b, "shell: %s\n", resp.Shell)
	}
	if resp.WorkingDir != "" {
		_, _ = fmt.Fprintf(&b, "working_dir: %s\n", resp.WorkingDir)
	}
	if resp.TimedOut {
		_, _ = fmt.Fprintf(&b, "status: timed_out\n")
	}
	_, _ = fmt.Fprintf(&b, "stdout:\n%s", resp.Stdout)
	if resp.Stderr != "" {
		_, _ = fmt.Fprintf(&b, "\nstderr:\n%s", resp.Stderr)
	}
	if resp.Truncated {
		_, _ = fmt.Fprintf(&b, "\n[note: output truncated to %d bytes]", maxOutputSize)
	}
	return b.String()
}

func resolveShell(ctx context.Context, requested string) (shellSpec, error) {
	kind := strings.ToLower(strings.TrimSpace(requested))
	if kind == "" {
		kind = "auto"
	}

	switch kind {
	case "auto":
		return resolveAutoShell(ctx)
	case "bash":
		return resolveExecutableShell("bash", []string{"-lc"}, "bash")
	case "pwsh":
		return resolvePwshShell()
	case "cmd":
		return resolveExecutableShell(windowsDefaultShell, []string{"/C"}, "cmd")
	case "git_bash":
		return resolveGitBashShell(ctx, "git_bash")
	case "mingw":
		return resolveMingwShell(ctx)
	default:
		return shellSpec{}, fmt.Errorf("unsupported shell %q; use auto, bash, pwsh, cmd, git_bash, or mingw", requested)
	}
}

func resolveAutoShell(ctx context.Context) (shellSpec, error) {
	return resolveAutoShellForOS(ctx, runtime.GOOS)
}

func resolveAutoShellForOS(ctx context.Context, goos string) (shellSpec, error) {
	if goos == "windows" {
		if spec, err := resolveGitBashShell(ctx, "git_bash"); err == nil {
			return spec, nil
		}
		if spec, err := resolvePwshShell(); err == nil {
			return spec, nil
		}
		return resolveExecutableShell(windowsDefaultShell, []string{"/C"}, "cmd")
	}
	if shell := toolEnvironmentVariable(ctx, "SHELL"); shell != "" {
		return shellSpec{Name: "bash", Path: shell, Args: []string{"-lc"}}, nil
	}
	if spec, err := resolveExecutableShell("bash", []string{"-lc"}, "bash"); err == nil {
		return spec, nil
	}
	return resolveExecutableShell(defaultShell, []string{"-c"}, "sh")
}

func resolvePwshShell() (shellSpec, error) {
	if path, err := exec.LookPath("pwsh"); err == nil {
		return shellSpec{Name: "pwsh", Path: path, Args: []string{"-NoProfile", "-NonInteractive", "-Command"}}, nil
	}
	if path, err := exec.LookPath("powershell"); err == nil {
		return shellSpec{Name: "pwsh", Path: path, Args: []string{"-NoProfile", "-NonInteractive", "-Command"}}, nil
	}
	return shellSpec{}, errors.New("pwsh shell requested but neither pwsh nor powershell was found")
}

func resolveGitBashShell(ctx context.Context, name string) (shellSpec, error) {
	for _, candidate := range gitBashCandidates(ctx) {
		if path, ok := executableCandidate(candidate); ok {
			return shellSpec{Name: name, Path: path, Args: []string{"-lc"}}, nil
		}
	}
	return shellSpec{}, errors.New("git_bash shell requested but Git Bash was not found")
}

func resolveMingwShell(ctx context.Context) (shellSpec, error) {
	for _, candidate := range mingwBashCandidates(ctx) {
		if path, ok := executableCandidate(candidate); ok {
			return shellSpec{Name: "mingw", Path: path, Args: []string{"-lc"}}, nil
		}
	}
	return resolveGitBashShell(ctx, "mingw")
}

func resolveExecutableShell(path string, args []string, name string) (shellSpec, error) {
	resolved, err := exec.LookPath(path)
	if err != nil {
		if filepath.IsAbs(path) {
			if _, statErr := os.Stat(path); statErr == nil {
				return shellSpec{Name: name, Path: path, Args: args}, nil
			}
		}
		return shellSpec{}, fmt.Errorf("%s shell requested but %q was not found", name, path)
	}
	return shellSpec{Name: name, Path: resolved, Args: args}, nil
}

func executableCandidate(path string) (string, bool) {
	if strings.TrimSpace(path) == "" {
		return "", false
	}
	if resolved, err := exec.LookPath(path); err == nil {
		return resolved, true
	}
	if _, err := os.Stat(path); err == nil {
		return path, true
	}
	return "", false
}

func gitBashCandidates(ctx context.Context) []string {
	candidates := []string{
		toolEnvironmentVariable(ctx, "GIT_BASH"),
		"bash",
	}
	if runtime.GOOS == "windows" {
		candidates = append(candidates,
			filepath.Join(toolEnvironmentVariable(ctx, "ProgramFiles"), "Git", "bin", "bash.exe"),
			filepath.Join(toolEnvironmentVariable(ctx, "ProgramFiles"), "Git", "usr", "bin", "bash.exe"),
			filepath.Join(toolEnvironmentVariable(ctx, "ProgramFiles(x86)"), "Git", "bin", "bash.exe"),
			filepath.Join(toolEnvironmentVariable(ctx, "ProgramFiles(x86)"), "Git", "usr", "bin", "bash.exe"),
		)
	}
	return candidates
}

func mingwBashCandidates(ctx context.Context) []string {
	candidates := []string{
		toolEnvironmentVariable(ctx, "MSYS2_BASH"),
		toolEnvironmentVariable(ctx, "MINGW_BASH"),
	}
	if runtime.GOOS == "windows" {
		candidates = append(candidates,
			`C:\msys64\usr\bin\bash.exe`,
			`C:\msys64\mingw64\bin\bash.exe`,
			`C:\msys32\usr\bin\bash.exe`,
			`C:\msys32\mingw32\bin\bash.exe`,
		)
	}
	return candidates
}

func normalizeBashTimeout(ctx context.Context, timeoutMilliseconds int) (time.Duration, error) {
	if timeoutMilliseconds <= 0 {
		configured := toolEnvironmentVariable(ctx, bashToolTimeoutEnv)
		if configured == "" {
			return defaultBashTimeout, nil
		}
		value, err := strconv.Atoi(configured)
		if err != nil || value <= 0 {
			return 0, fmt.Errorf("%s must be a positive integer", bashToolTimeoutEnv)
		}
		timeoutMilliseconds = value
	}
	timeout := time.Duration(timeoutMilliseconds) * time.Millisecond
	if timeout > maxBashTimeout {
		return maxBashTimeout, nil
	}
	return timeout, nil
}

func validateBashCommand(ctx context.Context, command string) error {
	allowList := toolEnvironmentVariable(ctx, bashToolAllowListEnv)
	if allowList == "" {
		return nil
	}

	allowedCommands := strings.Split(allowList, ",")
	for i, cmd := range allowedCommands {
		allowedCommands[i] = strings.TrimSpace(cmd)
	}

	firstWord := strings.Fields(command)[0]
	if slices.Contains(allowedCommands, firstWord) {
		return nil
	}

	return fmt.Errorf("command %q is not in the allowed list: %s", firstWord, allowList)
}

func commandEnvironment(ctx context.Context) ([]string, error) {
	environment := make([]string, 0, len(os.Environ())+len(core.EnvironmentFromContext(ctx)))
	indexes := map[string]int{}
	for _, entry := range os.Environ() {
		name, _, found := strings.Cut(entry, "=")
		if found && name != "" && isSensitiveToolEnvironmentName(name) {
			continue
		}
		environment = append(environment, entry)
		if found && name != "" {
			indexes[normalizedEnvironmentName(name)] = len(environment) - 1
		}
	}

	configured := core.EnvironmentFromContext(ctx)
	keys := make([]string, 0, len(configured))
	for key := range configured {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := configured[key]
		if key == "" || strings.ContainsAny(key, "=\x00") {
			return nil, fmt.Errorf("invalid environment variable name %q", key)
		}
		if strings.ContainsRune(value, '\x00') {
			return nil, fmt.Errorf("environment variable %q contains a null byte", key)
		}
		if isSensitiveToolEnvironmentName(key) {
			continue
		}
		entry := key + "=" + value
		normalized := normalizedEnvironmentName(key)
		if index, exists := indexes[normalized]; exists {
			environment[index] = entry
			continue
		}
		indexes[normalized] = len(environment)
		environment = append(environment, entry)
	}
	return environment, nil
}

func normalizedEnvironmentName(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}

func isSensitiveToolEnvironmentName(name string) bool {
	upper := strings.ToUpper(strings.TrimSpace(name))
	return strings.Contains(upper, "KEY") ||
		strings.Contains(upper, "TOKEN") ||
		strings.Contains(upper, "SECRET") ||
		strings.Contains(upper, "PASSWORD")
}
