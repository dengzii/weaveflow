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
	"strings"
	"time"

	"github.com/tmc/langchaingo/llms"
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
	Description               string `json:"description,omitempty"`
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
						"description": "Optional shell runtime. auto defaults to pwsh/cmd on Windows and bash/sh elsewhere.",
					},
				},
				"required":             []string{"command"},
				"additionalProperties": false,
			},
		},
		Handler: bashTool,
	}
}

func bashTool(ctx context.Context, input string) (string, error) {
	var req bashRequest
	if err := decodeToolRequest(input, "bash", &req); err != nil {
		return "", err
	}

	command := strings.TrimSpace(req.Command)
	if command == "" {
		return "", errors.New("command is required")
	}

	if err := validateBashCommand(command); err != nil {
		return "", err
	}
	if req.RunInBackground {
		return "", errors.New("run_in_background is not supported by this Bash tool")
	}

	timeout := normalizeBashTimeout(req.Timeout)
	workingDir, err := toolWorkspaceDir()
	if err != nil {
		return "", err
	}

	result, err := executeBashCommand(ctx, command, workingDir, timeout, req.Shell)
	if err != nil {
		return "", err
	}

	return formatBashResponse(result), nil
}

func executeBashCommand(ctx context.Context, command, workingDir string, timeout time.Duration, requestedShell string) (*bashResponse, error) {
	shell, err := resolveShell(requestedShell)
	if err != nil {
		return nil, err
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := append([]string{}, shell.Args...)
	args = append(args, command)
	cmd := exec.CommandContext(execCtx, shell.Path, args...)
	cmd.Dir = workingDir

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

func resolveShell(requested string) (shellSpec, error) {
	kind := strings.ToLower(strings.TrimSpace(requested))
	if kind == "" {
		kind = "auto"
	}

	switch kind {
	case "auto":
		return resolveAutoShell()
	case "bash":
		return resolveExecutableShell("bash", []string{"-lc"}, "bash")
	case "pwsh":
		return resolvePwshShell()
	case "cmd":
		return resolveExecutableShell(windowsDefaultShell, []string{"/C"}, "cmd")
	case "git_bash":
		return resolveGitBashShell("git_bash")
	case "mingw":
		return resolveMingwShell()
	default:
		return shellSpec{}, fmt.Errorf("unsupported shell %q; use auto, bash, pwsh, cmd, git_bash, or mingw", requested)
	}
}

func resolveAutoShell() (shellSpec, error) {
	if runtime.GOOS == "windows" {
		if spec, err := resolvePwshShell(); err == nil {
			return spec, nil
		}
		return resolveExecutableShell(windowsDefaultShell, []string{"/C"}, "cmd")
	}
	if shell := os.Getenv("SHELL"); shell != "" {
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

func resolveGitBashShell(name string) (shellSpec, error) {
	for _, candidate := range gitBashCandidates() {
		if path, ok := executableCandidate(candidate); ok {
			return shellSpec{Name: name, Path: path, Args: []string{"-lc"}}, nil
		}
	}
	return shellSpec{}, errors.New("git_bash shell requested but Git Bash was not found")
}

func resolveMingwShell() (shellSpec, error) {
	for _, candidate := range mingwBashCandidates() {
		if path, ok := executableCandidate(candidate); ok {
			return shellSpec{Name: "mingw", Path: path, Args: []string{"-lc"}}, nil
		}
	}
	return resolveGitBashShell("mingw")
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

func gitBashCandidates() []string {
	candidates := []string{
		os.Getenv("GIT_BASH"),
		"bash",
	}
	if runtime.GOOS == "windows" {
		candidates = append(candidates,
			filepath.Join(os.Getenv("ProgramFiles"), "Git", "bin", "bash.exe"),
			filepath.Join(os.Getenv("ProgramFiles"), "Git", "usr", "bin", "bash.exe"),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Git", "bin", "bash.exe"),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Git", "usr", "bin", "bash.exe"),
		)
	}
	return candidates
}

func mingwBashCandidates() []string {
	candidates := []string{
		os.Getenv("MSYS2_BASH"),
		os.Getenv("MINGW_BASH"),
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

func normalizeBashTimeout(timeoutMilliseconds int) time.Duration {
	if timeoutMilliseconds <= 0 {
		return defaultBashTimeout
	}
	d := time.Duration(timeoutMilliseconds) * time.Millisecond
	if d > maxBashTimeout {
		return maxBashTimeout
	}
	return d
}

func validateBashCommand(command string) error {
	allowList := os.Getenv(bashToolAllowListEnv)
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
