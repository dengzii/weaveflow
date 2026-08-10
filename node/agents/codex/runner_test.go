package codex

import (
	"reflect"
	"testing"
)

func TestCodexArgumentsKeepApprovalBeforeExec(t *testing.T) {
	config := resolvedCodexRun{
		resolvedCodexRunConfig: resolvedCodexRunConfig{
			resolvedCodexRunnerConfig: resolvedCodexRunnerConfig{RunnerConfig: RunnerConfig{Sandbox: SandboxWorkspaceWrite}},
			workspacePath:             `C:\workspace`,
			model:                     "graph-model",
		},
	}
	got := codexArguments(config)
	want := []string{
		"--ask-for-approval", "never",
		"exec",
		"--json",
		"--ephemeral",
		"--ignore-user-config",
		"--ignore-rules",
		"--strict-config",
		"--sandbox", "workspace-write",
		"--cd", `C:\workspace`,
		"--model", "graph-model",
		"-",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments = %#v", got)
	}
}

func TestCodexArgumentsConfigureBaseURLBeforeExec(t *testing.T) {
	config := resolvedCodexRun{
		resolvedCodexRunConfig: resolvedCodexRunConfig{
			resolvedCodexRunnerConfig: resolvedCodexRunnerConfig{RunnerConfig: RunnerConfig{Sandbox: SandboxReadOnly}},
			workspacePath:             `C:\workspace`,
			baseURL:                   "https://gateway.example/v1",
		},
	}
	got := codexArguments(config)
	want := []string{
		"--ask-for-approval", "never",
		"-c", `openai_base_url="https://gateway.example/v1"`,
		"exec",
		"--json",
		"--ephemeral",
		"--ignore-user-config",
		"--ignore-rules",
		"--strict-config",
		"--sandbox", "read-only",
		"--cd", `C:\workspace`,
		"-",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments = %#v", got)
	}
}

func TestValidateCodexCapabilitiesRequiresUsedOptions(t *testing.T) {
	rootHelp := "--ask-for-approval --config"
	execHelp := "--json --ephemeral --ignore-user-config --ignore-rules --strict-config --sandbox --cd --model"
	if err := validateCodexHelp(rootHelp, execHelp); err != nil {
		t.Fatal(err)
	}
	if err := validateCodexHelp("--ask-for-approval", execHelp); err == nil {
		t.Fatal("expected missing top-level option error")
	}
	if err := validateCodexHelp(rootHelp, "--json"); err == nil {
		t.Fatal("expected missing option error")
	}
}
