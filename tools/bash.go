package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/tmc/langchaingo/llms"
)

const (
	bashToolWorkspaceEnv = "WEAVEFLOW_TOOL_WORKDIR"
	bashToolTimeoutEnv   = "WEAVEFLOW_BASH_TIMEOUT"
	bashToolAllowListEnv = "WEAVEFLOW_BASH_ALLOWLIST"
	defaultBashTimeout   = 30 * time.Second
	maxBashTimeout       = 5 * time.Minute
	maxOutputSize        = 64 * 1024
	defaultShell         = "/bin/sh"
	windowsDefaultShell  = "cmd.exe"
)

type bashRequest struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

type bashResponse struct {
	Command    string `json:"command"`
	ExitCode   int    `json:"exit_code"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	Truncated  bool   `json:"truncated,omitempty"`
	TimedOut   bool   `json:"timed_out,omitempty"`
	WorkingDir string `json:"working_dir,omitempty"`
}

func NewBash() Tool {
	return Tool{
		Function: &llms.FunctionDefinition{
			Name:        "bash",
			Description: "Execute a bash/shell command and return the output. Commands run in a sandboxed workspace directory. Use with caution as it can modify files.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "The shell command to execute.",
					},
					"timeout": map[string]any{
						"type":        "integer",
						"description": "Optional timeout in seconds. Default 30, max 300.",
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
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		req.Command = strings.TrimSpace(input)
	}

	command := strings.TrimSpace(req.Command)
	if command == "" {
		return "", errors.New("command is required")
	}

	if err := validateBashCommand(command); err != nil {
		return "", err
	}

	timeout := normalizeBashTimeout(req.Timeout)
	workingDir := getBashWorkingDir()

	result, err := executeBashCommand(ctx, command, workingDir, timeout)
	if err != nil {
		return "", err
	}

	return formatBashResponse(result), nil
}

func executeBashCommand(ctx context.Context, command, workingDir string, timeout time.Duration) (*bashResponse, error) {
	shell := getShell()
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, shell, "-c", command)
	cmd.Dir = workingDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	exitCode := 0
	timedOut := false

	if err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			timedOut = true
			exitCode = -1
		} else if exitErr, ok := err.(*exec.ExitError); ok {
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
	fmt.Fprintf(&b, "exit_code: %d\n", resp.ExitCode)
	if resp.WorkingDir != "" {
		fmt.Fprintf(&b, "working_dir: %s\n", resp.WorkingDir)
	}
	if resp.TimedOut {
		fmt.Fprintf(&b, "status: timed_out\n")
	}
	fmt.Fprintf(&b, "stdout:\n%s", resp.Stdout)
	if resp.Stderr != "" {
		fmt.Fprintf(&b, "\nstderr:\n%s", resp.Stderr)
	}
	if resp.Truncated {
		fmt.Fprintf(&b, "\n[note: output truncated to %d bytes]", maxOutputSize)
	}
	return b.String()
}

func getShell() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	if _, err := exec.LookPath(defaultShell); err == nil {
		return defaultShell
	}
	return windowsDefaultShell
}

func getBashWorkingDir() string {
	if dir := os.Getenv(bashToolWorkspaceEnv); dir != "" {
		return dir
	}
	if dir, err := os.Getwd(); err == nil {
		return dir
	}
	return "."
}

func normalizeBashTimeout(timeoutSeconds int) time.Duration {
	if timeoutSeconds <= 0 {
		return defaultBashTimeout
	}
	d := time.Duration(timeoutSeconds) * time.Second
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
