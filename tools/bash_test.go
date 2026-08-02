package tools

import (
	"context"
	"strings"
	"testing"
)

func TestBashToolAutoShellExecutesCommand(t *testing.T) {
	out, err := bashTool(context.Background(), `{"command":"echo hello","shell":"auto","timeout":5000}`)
	if err != nil {
		t.Fatalf("bashTool: %v", err)
	}
	if !strings.Contains(out, "exit_code: 0") {
		t.Fatalf("expected successful exit, got:\n%s", out)
	}
	if !strings.Contains(out, "shell: ") {
		t.Fatalf("expected shell in response, got:\n%s", out)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("expected command output, got:\n%s", out)
	}
}

func TestBashToolRejectsRawInput(t *testing.T) {
	if _, err := bashTool(context.Background(), "echo hello"); err == nil {
		t.Fatal("raw Bash input was accepted")
	}
}

func TestBashToolUsesConfiguredWorkspace(t *testing.T) {
	root := t.TempDir()
	t.Setenv(toolWorkspaceEnv, root)

	out, err := bashTool(context.Background(), `{"command":"echo hello","shell":"auto"}`)
	if err != nil {
		t.Fatalf("bashTool: %v", err)
	}
	if !strings.Contains(out, "working_dir: "+root) {
		t.Fatalf("expected configured working directory, got: %s", out)
	}
}

func TestBashToolRejectsUnsupportedShell(t *testing.T) {
	_, err := bashTool(context.Background(), `{"command":"echo hello","shell":"fish"}`)
	if err == nil {
		t.Fatal("expected unsupported shell error")
	}
	if !strings.Contains(err.Error(), "unsupported shell") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBashToolRejectsBackgroundMode(t *testing.T) {
	_, err := bashTool(context.Background(), `{"command":"echo hello","run_in_background":true}`)
	if err == nil {
		t.Fatal("expected background mode error")
	}
	if !strings.Contains(err.Error(), "run_in_background") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveShellRejectsAliases(t *testing.T) {
	for _, alias := range []string{"powershell", "git-bash", "gitbash", "msys", "msys2"} {
		t.Run(alias, func(t *testing.T) {
			if _, err := resolveShell(alias); err == nil {
				t.Fatalf("shell alias %q was accepted", alias)
			}
		})
	}
}
