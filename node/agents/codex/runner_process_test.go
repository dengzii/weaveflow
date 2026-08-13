package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/core"
)

const helperEnvironment = "WEAVEFLOW_CODEX_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(helperEnvironment) == "1" {
		runCodexHelperProcess()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestProcessRunnerExecutesJSONLProtocol(t *testing.T) {
	runner, ctx := newHelperProcessRunner(t, nil, nil)
	var chunks []Chunk
	result, err := runner.Run(ctx, RunRequest{
		ModelID: "test",
		Prompt:  "review from stdin",
		OnChunk: func(chunk Chunk) error {
			chunks = append(chunks, chunk)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ThreadID != "helper-thread" || result.Output != "review from stdin" || result.ExitCode != 0 {
		t.Fatalf("result = %#v", result)
	}
	if result.Usage.InputTokens != 4 || result.Usage.OutputTokens != 2 {
		t.Fatalf("usage = %#v", result.Usage)
	}
	if len(chunks) != 1 || chunks[0].ModelID != "test" || chunks[0].ThreadID != "helper-thread" {
		t.Fatalf("chunks = %#v", chunks)
	}
}

func TestProcessRunnerWaitsForJSONLReaderBeforeClosingPipe(t *testing.T) {
	runner, ctx := newHelperProcessRunner(t, nil, nil)
	result, err := runner.Run(ctx, RunRequest{
		ModelID: "test",
		Prompt:  "slow reader",
		OnChunk: func(Chunk) error {
			time.Sleep(100 * time.Millisecond)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "slow reader" || result.ExitCode != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestProcessRunnerAllowsRecoverableErrorBeforeCompletion(t *testing.T) {
	runner, ctx := newHelperProcessRunner(t, nil, nil)
	result, err := runner.Run(ctx, RunRequest{ModelID: "test", Prompt: "recover"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "recover" || result.ExitCode != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestProcessRunnerRejectsMissingTerminalEvent(t *testing.T) {
	runner, ctx := newHelperProcessRunner(t, nil, nil)
	_, err := runner.Run(ctx, RunRequest{ModelID: "test", Prompt: "incomplete"})
	if err == nil || !strings.Contains(err.Error(), "completed without turn.completed after error: connection failed") {
		t.Fatalf("error = %v", err)
	}
}

func TestProcessRunnerRedactsAllowedEnvironmentValues(t *testing.T) {
	runner, ctx := newHelperProcessRunner(t, map[string]string{
		"WEAVEFLOW_CODEX_TEST_SECRET": "super-secret-token",
	}, nil)
	result, err := runner.Run(ctx, RunRequest{ModelID: "test", Prompt: "fail"})
	if err == nil {
		t.Fatal("expected runner error")
	}
	if strings.Contains(err.Error(), "super-secret-token") || strings.Contains(result.Stderr, "super-secret-token") {
		t.Fatalf("secret leaked: error = %v, stderr = %q", err, result.Stderr)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") || !strings.Contains(result.Stderr, "[REDACTED]") {
		t.Fatalf("redaction missing: error = %v, stderr = %q", err, result.Stderr)
	}
}

func TestProcessRunnerRedactsConfiguredAPIKey(t *testing.T) {
	runner, ctx := newHelperProcessRunner(t, nil, &core.ModelConfig{
		ID:     "test",
		APIKey: "graph-settings-secret-token",
	})
	result, err := runner.Run(ctx, RunRequest{ModelID: "test", Prompt: "fail"})
	if err == nil {
		t.Fatal("expected runner error")
	}
	if strings.Contains(err.Error(), "graph-settings-secret-token") || strings.Contains(result.Stderr, "graph-settings-secret-token") {
		t.Fatalf("configured API key leaked: error = %v, stderr = %q", err, result.Stderr)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") || !strings.Contains(result.Stderr, "[REDACTED]") {
		t.Fatalf("configured API key redaction missing: error = %v, stderr = %q", err, result.Stderr)
	}
}

func TestProcessRunnerRejectsUnknownModel(t *testing.T) {
	runner, ctx := newHelperProcessRunner(t, nil, nil)
	_, err := runner.Run(ctx, RunRequest{ModelID: "missing", Prompt: "review"})
	if err == nil || !strings.Contains(err.Error(), `model "missing" is not configured`) {
		t.Fatalf("error = %v", err)
	}
}

func newHelperProcessRunner(t *testing.T, environment map[string]string, modelConfig *core.ModelConfig) (*ProcessRunner, context.Context) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	runner, err := newProcessRunner(RunnerConfig{
		Executable:            executable,
		Sandbox:               SandboxReadOnly,
		AllowedWorkspaceRoots: []string{workspace},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	graphEnvironment := map[string]string{
		helperEnvironment:         "1",
		codexWorkspaceEnvironment: workspace,
	}
	for name, value := range environment {
		graphEnvironment[name] = value
	}
	ctx := core.WithEnvironment(context.Background(), graphEnvironment)
	if modelConfig == nil {
		modelConfig = &core.ModelConfig{ID: "test", Provider: "openai", APIFormat: "responses", Model: "graph-model"}
	}
	if modelConfig.Provider == "" {
		modelConfig.Provider = "openai"
	}
	if modelConfig.APIFormat == "" {
		modelConfig.APIFormat = "responses"
	}
	ctx = core.WithModelConfigs(ctx, map[string]core.ModelConfig{modelConfig.ID: *modelConfig})
	return runner, ctx
}

func runCodexHelperProcess() {
	prompt, _ := io.ReadAll(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	_ = encoder.Encode(map[string]any{"type": "thread.started", "thread_id": "helper-thread"})
	if string(prompt) == "fail" {
		secret := os.Getenv("WEAVEFLOW_CODEX_TEST_SECRET")
		if secret == "" {
			secret = os.Getenv("OPENAI_API_KEY")
		}
		_, _ = fmt.Fprintln(os.Stderr, "stderr "+secret)
		_ = encoder.Encode(map[string]any{"type": "turn.failed", "error": map[string]any{"message": "failed " + secret}})
		os.Exit(2)
	}
	if string(prompt) == "recover" {
		_ = encoder.Encode(map[string]any{"type": "error", "message": "Reconnecting... 5/5"})
	}
	if string(prompt) == "incomplete" {
		_ = encoder.Encode(map[string]any{"type": "error", "message": "connection failed"})
		return
	}
	_ = encoder.Encode(map[string]any{
		"type": "item.completed",
		"item": map[string]any{"type": "agent_message", "text": string(prompt)},
	})
	_ = encoder.Encode(map[string]any{
		"type":  "turn.completed",
		"usage": map[string]any{"input_tokens": 4, "output_tokens": 2},
	})
}
