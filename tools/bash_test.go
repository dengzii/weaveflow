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

func TestBashToolRawInputStillWorks(t *testing.T) {
	out, err := bashTool(context.Background(), "echo hello")
	if err != nil {
		t.Fatalf("bashTool: %v", err)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("expected command output, got:\n%s", out)
	}
}

func TestBashToolUsesConfiguredWorkspace(t *testing.T) {
	root := t.TempDir()
	t.Setenv(toolWorkspaceEnv, root)

	out, err := bashTool(context.Background(), "echo hello")
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

func TestResolveShellSupportsAliases(t *testing.T) {
	tests := []string{"", "auto", "pwsh", "powershell", "cmd", "bash", "git-bash", "gitbash", "msys", "msys2"}
	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			_, _ = resolveShell(tt)
		})
	}
}
