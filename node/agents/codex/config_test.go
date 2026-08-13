package codex

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/core"
)

func TestResolveCodexRunnerConfigAppliesSafeDefaults(t *testing.T) {
	config, err := resolveCodexRunnerConfig(RunnerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if config.Executable != "codex" || config.Sandbox != SandboxWorkspaceWrite {
		t.Fatalf("config = %#v", config)
	}
	if len(config.AllowedWorkspaceRoots) != 1 || !filepath.IsAbs(config.AllowedWorkspaceRoots[0]) {
		t.Fatalf("allowed workspace roots = %#v", config.AllowedWorkspaceRoots)
	}
	if config.TimeoutSeconds != defaultTimeoutSeconds || config.MaxStdoutBytes != defaultMaxStdoutBytes || config.MaxStderrBytes != defaultMaxStderrBytes || config.MaxConcurrency != defaultMaxConcurrency {
		t.Fatalf("defaults = %#v", config.RunnerConfig)
	}
}

func TestResolveCodexRunnerConfigRejectsEmptyAllowedRoots(t *testing.T) {
	_, err := resolveCodexRunnerConfig(RunnerConfig{AllowedWorkspaceRoots: []string{}})
	if err == nil || !strings.Contains(err.Error(), "allowed_workspace_roots") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunnerConfigFromEnvironment(t *testing.T) {
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	t.Setenv(codexExecutableEnvironment, "custom-codex")
	t.Setenv(codexSandboxEnvironment, string(SandboxReadOnly))
	t.Setenv(codexWorkspaceRootsEnvironment, strings.Join([]string{firstRoot, secondRoot}, string(os.PathListSeparator)))
	t.Setenv(codexTimeoutSecondsEnvironment, "60")
	t.Setenv(codexMaxStdoutBytesEnvironment, "1024")
	t.Setenv(codexMaxStderrBytesEnvironment, "512")
	t.Setenv(codexMaxConcurrencyEnvironment, "3")

	config, err := RunnerConfigFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if config.Executable != "custom-codex" || config.Sandbox != SandboxReadOnly || config.TimeoutSeconds != 60 || config.MaxStdoutBytes != 1024 || config.MaxStderrBytes != 512 || config.MaxConcurrency != 3 {
		t.Fatalf("config = %#v", config)
	}
	if len(config.AllowedWorkspaceRoots) != 2 || config.AllowedWorkspaceRoots[0] != firstRoot || config.AllowedWorkspaceRoots[1] != secondRoot {
		t.Fatalf("allowed roots = %#v", config.AllowedWorkspaceRoots)
	}
}

func TestRunnerConfigFromEnvironmentRejectsInvalidNumber(t *testing.T) {
	t.Setenv(codexTimeoutSecondsEnvironment, "invalid")
	if _, err := RunnerConfigFromEnvironment(); err == nil || !strings.Contains(err.Error(), codexTimeoutSecondsEnvironment) {
		t.Fatalf("error = %v", err)
	}
}

func TestNormalizeCodexBaseURL(t *testing.T) {
	baseURL, err := normalizeCodexBaseURL("  https://gateway.example/v1  ")
	if err != nil {
		t.Fatal(err)
	}
	if baseURL != "https://gateway.example/v1" {
		t.Fatalf("base_url = %q", baseURL)
	}
}

func TestNormalizeCodexBaseURLRejectsInvalidURL(t *testing.T) {
	_, err := normalizeCodexBaseURL("ftp://gateway.example/v1")
	if err == nil || !strings.Contains(err.Error(), "must use http or https") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateCodexModelConfigRequiresResponsesAPI(t *testing.T) {
	if err := validateCodexModelConfig("review", core.ModelConfig{Provider: "openai", APIFormat: "responses"}); err != nil {
		t.Fatal(err)
	}
	if err := validateCodexModelConfig("review", core.ModelConfig{Provider: "mistral", APIFormat: "responses"}); err == nil || !strings.Contains(err.Error(), "provider") {
		t.Fatalf("provider error = %v", err)
	}
	if err := validateCodexModelConfig("review", core.ModelConfig{Provider: "openai", APIFormat: "chat_completions"}); err == nil || !strings.Contains(err.Error(), "api_format") {
		t.Fatalf("API format error = %v", err)
	}
}

func TestResolveCodexExecutableUsesNativeWindowsBinary(t *testing.T) {
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("Codex is not installed")
	}
	path, err := resolveCodexExecutable("codex")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(path), ".exe") {
		t.Fatalf("executable = %q", path)
	}
}

func TestInstalledCodexSupportsRequiredCapabilities(t *testing.T) {
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("Codex is not installed")
	}
	executable, err := resolveCodexExecutable("codex")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCodexCapabilities(executable); err != nil {
		t.Fatal(err)
	}
}

func TestResolveCodexWorkspaceRejectsPathOutsideAllowedRoot(t *testing.T) {
	root := t.TempDir()
	workspace := t.TempDir()
	config, err := resolveCodexRunnerConfig(RunnerConfig{AllowedWorkspaceRoots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := core.NewContext(core.WithEnvironment(context.Background(), map[string]string{
		codexWorkspaceEnvironment: workspace,
	}))
	_, err = resolveCodexWorkspace(ctx, config)
	if err == nil || !strings.Contains(err.Error(), "outside allowed roots") {
		t.Fatalf("error = %v", err)
	}
}

func TestCodexEnvironmentUsesGraphSettings(t *testing.T) {
	workspace := t.TempDir()
	ctx := core.NewContext(core.WithEnvironment(context.Background(), map[string]string{
		codexWorkspaceEnvironment: workspace,
		"TASK_TOKEN":              "graph-secret-token",
	}))
	environment, secretValues, err := environment(ctx, "model-api-key")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(environment, "\n")
	if !strings.Contains(joined, codexWorkspaceEnvironment+"="+workspace) || !strings.Contains(joined, "OPENAI_API_KEY=model-api-key") || !strings.Contains(joined, "TASK_TOKEN=graph-secret-token") {
		t.Fatalf("environment = %#v", environment)
	}
	secrets := strings.Join(secretValues, "\n")
	if !strings.Contains(secrets, "model-api-key") || !strings.Contains(secrets, "graph-secret-token") {
		t.Fatalf("secret values = %#v", secretValues)
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
