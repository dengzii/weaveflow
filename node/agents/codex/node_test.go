package codex

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

type fakeCodexRunner struct {
	request RunRequest
	result  RunResult
	err     error
}

func (runner *fakeCodexRunner) Run(_ context.Context, request RunRequest) (RunResult, error) {
	runner.request = request
	if request.OnChunk != nil {
		if err := request.OnChunk(Chunk{ModelID: request.ModelID, ThreadID: "thread-1", Channel: "content", Text: "chunk"}); err != nil {
			return RunResult{}, err
		}
	}
	return runner.result, runner.err
}

func TestCodexNodeBuildPreservesGraphSettingsModelID(t *testing.T) {
	built, err := NodeTypeDefinition().Build(
		&registry.BuildContext{},
		resolvedCodexSpec("review"),
	)
	if err != nil {
		t.Fatal(err)
	}
	node, ok := built.(*Node)
	if !ok || node.ModelID != "review" {
		t.Fatalf("node = %#v", built)
	}
}

func TestCodexNodeExecutesRunnerAndWritesOutput(t *testing.T) {
	runner := &fakeCodexRunner{
		result: RunResult{
			ModelID:  "review",
			ThreadID: "thread-1",
			Output:   "review complete",
			Usage:    Usage{InputTokens: 11, OutputTokens: 7},
			Duration: 1500 * time.Millisecond,
		},
	}
	built, err := NodeTypeDefinition().Build(
		&registry.BuildContext{},
		resolvedCodexSpec("review"),
	)
	if err != nil {
		t.Fatal(err)
	}
	initial := state.FromShared(map[string]any{"request": map[string]any{"input": "review this repository"}})
	access := state.NewEditingAccess(initial)
	var events []fruntime.EventType
	var progressEvents []progressEvent
	ctx := fruntime.WithRunnerEventPublisher(context.Background(), func(eventType fruntime.EventType, payload any) error {
		events = append(events, eventType)
		progress, ok := payload.(progressEvent)
		if !ok {
			t.Fatalf("payload = %T, want codexProgressEvent", payload)
		}
		progressEvents = append(progressEvents, progress)
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
	if runner.request.Prompt != "review this repository" || len(events) != 3 {
		t.Fatalf("request = %#v, events = %#v", runner.request, events)
	}
	for _, eventType := range events {
		if eventType != fruntime.EventNodeCustom {
			t.Fatalf("event type = %q, want %q", eventType, fruntime.EventNodeCustom)
		}
	}
	if progressEvents[0].Kind != codexProgressKind || progressEvents[0].Event != codexProgressStarted || progressEvents[0].Status != "started" || progressEvents[0].ModelID != "review" {
		t.Fatalf("started progress = %#v", progressEvents[0])
	}
	if progressEvents[1].Event != codexProgressRunning || progressEvents[1].Status != "running" || progressEvents[1].Channel != "content" || progressEvents[1].Message != "chunk" || progressEvents[1].ThreadID != "thread-1" {
		t.Fatalf("running progress = %#v", progressEvents[1])
	}
	if progressEvents[2].Event != codexProgressCompleted || progressEvents[2].Status != "completed" || progressEvents[2].Usage == nil || progressEvents[2].Usage.InputTokens != 11 || progressEvents[2].DurationMS != 1500 {
		t.Fatalf("completed progress = %#v", progressEvents[2])
	}
}

func TestCodexNodeFailureDoesNotWriteOutput(t *testing.T) {
	runner := &fakeCodexRunner{
		result: RunResult{
			ModelID:  "review",
			ThreadID: "thread-1",
			Duration: 2 * time.Second,
		},
		err: errors.New("provider failed"),
	}
	built, err := NodeTypeDefinition().Build(
		&registry.BuildContext{},
		resolvedCodexSpec("review"),
	)
	if err != nil {
		t.Fatal(err)
	}
	access := state.NewEditingAccess(state.FromShared(map[string]any{"request": map[string]any{"input": "review"}}))
	var progressEvents []progressEvent
	ctx := fruntime.WithRunnerEventPublisher(context.Background(), func(_ fruntime.EventType, payload any) error {
		progressEvents = append(progressEvents, payload.(progressEvent))
		return nil
	})
	ctx = WithRunner(ctx, runner)
	if _, err := built.Execute(core.NewContext(ctx), access); err == nil {
		t.Fatal("expected execution error")
	}
	if _, ok := state.Read(access, state.NewRef[string](state.Shared("review", "result"))); ok {
		t.Fatal("output was written on failure")
	}
	if len(progressEvents) != 3 || progressEvents[2].Event != codexProgressFailed || progressEvents[2].Status != "failed" || progressEvents[2].Message != "provider failed" || progressEvents[2].DurationMS != 2000 {
		t.Fatalf("progress events = %#v", progressEvents)
	}
}

func resolvedCodexSpec(modelID string) registry.ResolvedNodeSpec {
	return registry.ResolvedNodeSpec{
		Spec: dsl.GraphNodeSpec{
			ID:     "review_code",
			Type:   NodeType,
			Config: map[string]any{"model_id": modelID},
		},
		State: map[string]registry.ResolvedStateBinding{
			"prompt": {Path: state.Shared("request", "input")},
			"output": {Path: state.Shared("review", "result")},
		},
	}
}
