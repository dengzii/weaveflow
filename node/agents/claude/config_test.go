package claude

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/core"
)

func TestResolveClaudeRunnerConfigAppliesReadOnlyDefaults(t *testing.T) {
	config, err := resolveClaudeRunnerConfig(RunnerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if config.Executable != "claude" || config.Access != WorkspaceAccessReadOnly {
		t.Fatalf("config = %#v", config)
	}
	if len(config.Tools) != 3 || strings.Join(config.Tools, ",") != "Read,Glob,Grep" {
		t.Fatalf("tools = %#v", config.Tools)
	}
	if config.MaxBudgetUSD != defaultMaxBudgetUSD || config.TimeoutSeconds != defaultTimeoutSeconds || config.MaxConcurrency != defaultMaxConcurrency {
		t.Fatalf("defaults = %#v", config.RunnerConfig)
	}
	if len(config.AllowedWorkspaceRoots) != 1 || !filepath.IsAbs(config.AllowedWorkspaceRoots[0]) {
		t.Fatalf("allowed workspace roots = %#v", config.AllowedWorkspaceRoots)
	}
}

func TestResolveClaudeRunnerConfigRejectsUnsafeNativeWindowsWrite(t *testing.T) {
	_, err := resolveClaudeRunnerConfig(RunnerConfig{Access: WorkspaceAccessWrite})
	if runtime.GOOS == "windows" {
		if err == nil || !strings.Contains(err.Error(), "native Windows") {
			t.Fatalf("error = %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunnerConfigFromEnvironment(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv(claudeExecutableEnvironment, "custom-claude")
	t.Setenv(claudeAccessEnvironment, string(WorkspaceAccessReadOnly))
	t.Setenv(claudeWorkspaceRootsEnvironment, workspace)
	t.Setenv(claudeTimeoutSecondsEnvironment, "60")
	t.Setenv(claudeMaxStdoutBytesEnvironment, "1024")
	t.Setenv(claudeMaxStderrBytesEnvironment, "512")
	t.Setenv(claudeMaxConcurrencyEnvironment, "3")
	t.Setenv(claudeModelEnvironment, "claude-sonnet-4-6")
	t.Setenv(claudeMaxBudgetUSDEnvironment, "2.5")
	t.Setenv(claudeToolsEnvironment, "Read, Glob")
	t.Setenv(claudeAllowedToolsEnvironment, "Read")
	t.Setenv(claudeDisallowedToolsEnvironment, "Bash, Edit")
	t.Setenv(claudeEnvironmentNamesEnvironment, "ANTHROPIC_API_KEY, CUSTOM_TOKEN")

	config, err := RunnerConfigFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if config.Executable != "custom-claude" || config.TimeoutSeconds != 60 || config.MaxStdoutBytes != 1024 || config.MaxStderrBytes != 512 || config.MaxConcurrency != 3 {
		t.Fatalf("config = %#v", config)
	}
	if config.Model != "claude-sonnet-4-6" || config.MaxBudgetUSD != 2.5 {
		t.Fatalf("model policy = %#v", config)
	}
	if strings.Join(config.Tools, ",") != "Read,Glob" || strings.Join(config.DisallowedTools, ",") != "Bash,Edit" {
		t.Fatalf("tool policy = %#v", config)
	}
}

func TestResolveClaudeWorkspaceRejectsPathOutsideAllowedRoot(t *testing.T) {
	root := t.TempDir()
	workspace := t.TempDir()
	config, err := resolveClaudeRunnerConfig(RunnerConfig{AllowedWorkspaceRoots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := core.WithEnvironment(context.Background(), map[string]string{claudeWorkspaceEnvironment: workspace})
	_, err = resolveClaudeWorkspace(ctx, config)
	if err == nil || !strings.Contains(err.Error(), "outside allowed roots") {
		t.Fatalf("error = %v", err)
	}
}

func TestClaudeEnvironmentUsesOnlyConfiguredHostVariables(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-secret-token")
	t.Setenv("UNRELATED_SECRET", "must-not-pass")
	config, err := resolveClaudeRunnerConfig(RunnerConfig{
		EnvironmentNames: []string{"ANTHROPIC_API_KEY"},
	})
	if err != nil {
		t.Fatal(err)
	}
	environment, secrets, err := environment(config)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(environment, "\n")
	if !strings.Contains(joined, "ANTHROPIC_API_KEY=anthropic-secret-token") || strings.Contains(joined, "UNRELATED_SECRET") {
		t.Fatalf("environment = %#v", environment)
	}
	if strings.Join(secrets, "\n") != "anthropic-secret-token" {
		t.Fatalf("secrets = %#v", secrets)
	}
}

func TestCanonicalDirectoryRequiresDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalDirectory(path); err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Fatalf("error = %v", err)
	}
}
