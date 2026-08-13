package claude

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/registry"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

type fakeClaudeRunner struct {
	request RunRequest
	result  RunResult
	err     error
}

func (runner *fakeClaudeRunner) Run(_ context.Context, request RunRequest) (RunResult, error) {
	runner.request = request
	if request.OnChunk != nil {
		if err := request.OnChunk(Chunk{Model: "claude-test", SessionID: "session-1", Channel: "content", Text: "chunk"}); err != nil {
			return RunResult{}, err
		}
	}
	return runner.result, runner.err
}

func TestClaudeNodeExecutesRunnerAndWritesOutput(t *testing.T) {
	runner := &fakeClaudeRunner{
		result: RunResult{
			Model:     "claude-test",
			SessionID: "session-1",
			Output:    "review complete",
			Usage:     Usage{InputTokens: 11, OutputTokens: 7},
			CostUSD:   0.02,
			NumTurns:  2,
			Duration:  1500 * time.Millisecond,
		},
	}
	built, err := NodeTypeDefinition().Build(&registry.BuildContext{}, resolvedClaudeSpec())
	if err != nil {
		t.Fatal(err)
	}
	access := state.NewEditingAccess(state.FromShared(map[string]any{"request": map[string]any{"input": "review this repository"}}))
	var progressEvents []claudeProgressEvent
	ctx := fruntime.WithRunnerEventPublisher(context.Background(), func(eventType fruntime.EventType, payload any) error {
		if eventType != fruntime.EventNodeCustom {
			t.Fatalf("event type = %q", eventType)
		}
		progressEvents = append(progressEvents, payload.(claudeProgressEvent))
		return nil
	})
	ctx = WithRunner(ctx, runner)
	if _, err := built.Execute(core.NewContext(ctx), access); err != nil {
		t.Fatal(err)
	}
	output, err := state.Get(access, state.NewRef[string](state.Shared("review", "result")))
	if err != nil || output != "review complete" {
		t.Fatalf("output = %q, error = %v", output, err)
	}
	if runner.request.Prompt != "review this repository" || len(progressEvents) != 3 {
		t.Fatalf("request = %#v, progress = %#v", runner.request, progressEvents)
	}
	if progressEvents[0].Kind != claudeProgressKind || progressEvents[1].Event != claudeProgressRunning || progressEvents[2].CostUSD != 0.02 || progressEvents[2].NumTurns != 2 {
		t.Fatalf("progress = %#v", progressEvents)
	}
}

func TestClaudeNodeFailureDoesNotWriteOutput(t *testing.T) {
	runner := &fakeClaudeRunner{
		result: RunResult{Model: "claude-test", SessionID: "session-1", Duration: 2 * time.Second},
		err:    errors.New("provider failed"),
	}
	built, err := NodeTypeDefinition().Build(&registry.BuildContext{}, resolvedClaudeSpec())
	if err != nil {
		t.Fatal(err)
	}
	access := state.NewEditingAccess(state.FromShared(map[string]any{"request": map[string]any{"input": "review"}}))
	var progressEvents []claudeProgressEvent
	ctx := fruntime.WithRunnerEventPublisher(context.Background(), func(_ fruntime.EventType, payload any) error {
		progressEvents = append(progressEvents, payload.(claudeProgressEvent))
		return nil
	})
	ctx = WithRunner(ctx, runner)
	if _, err := built.Execute(core.NewContext(ctx), access); err == nil {
		t.Fatal("expected execution error")
	}
	if _, ok := state.Read(access, state.NewRef[string](state.Shared("review", "result"))); ok {
		t.Fatal("output was written on failure")
	}
	if len(progressEvents) != 3 || progressEvents[2].Event != claudeProgressFailed || progressEvents[2].DurationMS != 2000 {
		t.Fatalf("progress = %#v", progressEvents)
	}
}

func resolvedClaudeSpec() registry.ResolvedNodeSpec {
	return registry.ResolvedNodeSpec{
		Spec: dsl.GraphNodeSpec{ID: "review_code", Type: NodeType},
		State: map[string]registry.ResolvedStateBinding{
			"prompt": {Path: state.Shared("request", "input")},
			"output": {Path: state.Shared("review", "result")},
		},
	}
}
