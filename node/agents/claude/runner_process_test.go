package claude

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

const helperEnvironment = "WEAVEFLOW_CLAUDE_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(helperEnvironment) == "1" {
		runClaudeHelperProcess()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestProcessRunnerExecutesStreamJSONProtocol(t *testing.T) {
	runner, ctx := newClaudeHelperProcessRunner(t, nil)
	var chunks []Chunk
	result, err := runner.Run(ctx, RunRequest{
		Prompt: "review from stdin",
		OnChunk: func(chunk Chunk) error {
			chunks = append(chunks, chunk)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionID != "helper-session" || result.Model != "claude-test" || result.Output != "review from stdin" || result.ExitCode != 0 {
		t.Fatalf("result = %#v", result)
	}
	if result.Usage.InputTokens != 4 || result.Usage.OutputTokens != 2 || result.CostUSD != 0.01 || result.NumTurns != 1 {
		t.Fatalf("metadata = %#v", result)
	}
	if len(chunks) != 1 || chunks[0].SessionID != "helper-session" || chunks[0].Text != "review from stdin" {
		t.Fatalf("chunks = %#v", chunks)
	}
}

func TestProcessRunnerWaitsForOutputReaderBeforeClosingPipe(t *testing.T) {
	runner, ctx := newClaudeHelperProcessRunner(t, nil)
	result, err := runner.Run(ctx, RunRequest{
		Prompt: "slow reader",
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

func TestProcessRunnerRejectsMissingResultEvent(t *testing.T) {
	runner, ctx := newClaudeHelperProcessRunner(t, nil)
	_, err := runner.Run(ctx, RunRequest{Prompt: "incomplete"})
	if err == nil || !strings.Contains(err.Error(), "without a successful result event") {
		t.Fatalf("error = %v", err)
	}
}

func TestProcessRunnerRedactsConfiguredAuthentication(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-secret-token")
	runner, ctx := newClaudeHelperProcessRunner(t, []string{"ANTHROPIC_API_KEY"})
	result, err := runner.Run(ctx, RunRequest{Prompt: "fail"})
	if err == nil {
		t.Fatal("expected runner error")
	}
	if strings.Contains(err.Error(), "anthropic-secret-token") || strings.Contains(result.Stderr, "anthropic-secret-token") {
		t.Fatalf("secret leaked: error = %v, stderr = %q", err, result.Stderr)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") || !strings.Contains(result.Stderr, "[REDACTED]") {
		t.Fatalf("redaction missing: error = %v, stderr = %q", err, result.Stderr)
	}
}

func TestProcessRunnerRedactsProgressChunks(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-secret-token")
	runner, ctx := newClaudeHelperProcessRunner(t, []string{"ANTHROPIC_API_KEY"})
	var chunks []Chunk
	result, err := runner.Run(ctx, RunRequest{
		Prompt: "leak",
		OnChunk: func(chunk Chunk) error {
			chunks = append(chunks, chunk)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 || chunks[0].Text != "[REDACTED]" || result.Output != "[REDACTED]" {
		t.Fatalf("chunks = %#v, result = %#v", chunks, result)
	}
}

func newClaudeHelperProcessRunner(t *testing.T, environmentNames []string) (*ProcessRunner, context.Context) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if environmentNames == nil {
		environmentNames = []string{helperEnvironment}
	} else {
		environmentNames = append(environmentNames, helperEnvironment)
	}
	t.Setenv(helperEnvironment, "1")
	runner, err := newProcessRunner(RunnerConfig{
		Executable:            executable,
		Access:                WorkspaceAccessReadOnly,
		AllowedWorkspaceRoots: []string{workspace},
		EnvironmentNames:      environmentNames,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx := core.WithEnvironment(context.Background(), map[string]string{claudeWorkspaceEnvironment: workspace})
	return runner, ctx
}

func runClaudeHelperProcess() {
	prompt, _ := io.ReadAll(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	_ = encoder.Encode(map[string]any{"type": "system", "subtype": "init", "session_id": "helper-session", "model": "claude-test"})
	if string(prompt) == "fail" {
		secret := os.Getenv("ANTHROPIC_API_KEY")
		_, _ = fmt.Fprintln(os.Stderr, "stderr "+secret)
		_ = encoder.Encode(map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true, "result": "failed " + secret})
		os.Exit(2)
	}
	if string(prompt) == "incomplete" {
		return
	}
	output := string(prompt)
	if output == "leak" {
		output = os.Getenv("ANTHROPIC_API_KEY")
	}
	_ = encoder.Encode(map[string]any{
		"type":       "assistant",
		"session_id": "helper-session",
		"message": map[string]any{
			"model":   "claude-test",
			"content": []map[string]any{{"type": "text", "text": output}},
		},
	})
	_ = encoder.Encode(map[string]any{
		"type":           "result",
		"subtype":        "success",
		"session_id":     "helper-session",
		"result":         output,
		"total_cost_usd": 0.01,
		"num_turns":      1,
		"usage":          map[string]any{"input_tokens": 4, "output_tokens": 2},
	})
}
