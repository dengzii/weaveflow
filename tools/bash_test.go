package tools

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/llms"
)

func TestBashToolAutoShellExecutesCommand(t *testing.T) {
	result, err := NewBash().Handler(context.Background(), toolCallForTest("bash", `{"command":"echo hello","description":"Print a greeting","shell":"auto","timeout":5000}`))
	if err != nil {
		t.Fatalf("bash tool: %v", err)
	}
	out := result.Content
	if !strings.Contains(out, "exit_code: 0") {
		t.Fatalf("expected successful exit, got:\n%s", out)
	}
	if !strings.Contains(out, "shell: ") {
		t.Fatalf("expected shell in response, got:\n%s", out)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("expected command output, got:\n%s", out)
	}
	if runtime.GOOS == "windows" {
		if _, gitBashErr := resolveGitBashShell(context.Background(), "git_bash"); gitBashErr == nil && !strings.Contains(out, "shell: git_bash") {
			t.Fatalf("expected auto shell to use Git Bash, got:\n%s", out)
		}
	}
}

func TestBashToolRejectsInvalidArguments(t *testing.T) {
	if _, err := NewBash().Handler(context.Background(), toolCallForTest("bash", "echo hello")); err == nil {
		t.Fatal("invalid Bash arguments were accepted")
	}
}

func TestBashToolUsesConfiguredWorkspace(t *testing.T) {
	root := t.TempDir()
	t.Setenv(toolWorkspaceEnv, t.TempDir())
	ctx := core.WithEnvironment(context.Background(), map[string]string{toolWorkspaceEnv: root})

	result, err := NewBash().Handler(ctx, toolCallForTest("bash", `{"command":"echo hello","description":"Print a greeting","shell":"auto"}`))
	if err != nil {
		t.Fatalf("bash tool: %v", err)
	}
	out := result.Content
	if !strings.Contains(out, "working_dir: "+root) {
		t.Fatalf("expected configured working directory, got: %s", out)
	}
}

func TestBashToolRejectsUnsupportedShell(t *testing.T) {
	_, err := NewBash().Handler(context.Background(), toolCallForTest("bash", `{"command":"echo hello","description":"Print a greeting","shell":"fish"}`))
	if err == nil {
		t.Fatal("expected unsupported shell error")
	}
	if !strings.Contains(err.Error(), "unsupported shell") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBashToolRejectsBackgroundMode(t *testing.T) {
	_, err := NewBash().Handler(context.Background(), toolCallForTest("bash", `{"command":"echo hello","description":"Print a greeting","run_in_background":true}`))
	if err == nil {
		t.Fatal("expected background mode error")
	}
	if !strings.Contains(err.Error(), "run_in_background") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func toolCallForTest(name string, arguments string) llms.ToolCall {
	return llms.ToolCall{
		ID:   "test-call",
		Type: "function",
		FunctionCall: &llms.FunctionCall{
			Name:      name,
			Arguments: json.RawMessage(arguments),
		},
	}
}

func TestResolveShellRejectsAliases(t *testing.T) {
	for _, alias := range []string{"powershell", "git-bash", "gitbash", "msys", "msys2"} {
		t.Run(alias, func(t *testing.T) {
			if _, err := resolveShell(context.Background(), alias); err == nil {
				t.Fatalf("shell alias %q was accepted", alias)
			}
		})
	}
}

func TestResolveAutoShellPrefersGitBashOnWindows(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	t.Setenv("GIT_BASH", executable)
	ctx := core.WithEnvironment(context.Background(), map[string]string{"GIT_BASH": executable})

	shell, err := resolveAutoShellForOS(ctx, "windows")
	if err != nil {
		t.Fatalf("resolve auto shell: %v", err)
	}
	if shell.Name != "git_bash" {
		t.Fatalf("expected Git Bash, got %q", shell.Name)
	}
	if shell.Path != executable {
		t.Fatalf("expected Git Bash path %q, got %q", executable, shell.Path)
	}
}

func TestBashPolicyUsesContextEnvironment(t *testing.T) {
	t.Setenv(bashToolAllowListEnv, "")
	t.Setenv(bashToolTimeoutEnv, "")
	ctx := core.WithEnvironment(context.Background(), map[string]string{
		bashToolAllowListEnv: "echo",
		bashToolTimeoutEnv:   "2500",
	})

	if err := validateBashCommand(ctx, "pwd"); err == nil {
		t.Fatal("context allowlist did not reject command")
	}
	timeout, err := normalizeBashTimeout(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if timeout != 2500*time.Millisecond {
		t.Fatalf("timeout = %s, want 2.5s", timeout)
	}
}

func TestCommandEnvironmentScopesValuesAndRemovesSecrets(t *testing.T) {
	t.Setenv("WEAVEFLOW_PROCESS_SECRET", "process-secret")
	t.Setenv("WEAVEFLOW_CONTEXT_VALUE", "process-value")
	ctx := core.WithEnvironment(context.Background(), map[string]string{
		"WEAVEFLOW_CONTEXT_VALUE": "context-value",
		"WEAVEFLOW_CONTEXT_TOKEN": "context-secret",
	})

	environment, err := commandEnvironment(ctx)
	if err != nil {
		t.Fatal(err)
	}
	values := environmentValues(environment)
	if got := values["WEAVEFLOW_CONTEXT_VALUE"]; got != "context-value" {
		t.Fatalf("WEAVEFLOW_CONTEXT_VALUE = %q, want context-value", got)
	}
	for _, name := range []string{"WEAVEFLOW_PROCESS_SECRET", "WEAVEFLOW_CONTEXT_TOKEN"} {
		if _, exists := values[name]; exists {
			t.Fatalf("sensitive environment variable %s was passed to Bash", name)
		}
	}
}

func environmentValues(environment []string) map[string]string {
	values := map[string]string{}
	for _, entry := range environment {
		name, value, found := strings.Cut(entry, "=")
		if found && name != "" {
			values[name] = value
		}
	}
	return values
}
