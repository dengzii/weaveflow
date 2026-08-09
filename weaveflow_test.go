package weaveflow

import (
	"context"
	"errors"
	"testing"

	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

func TestNewRunnerAppliesDefaults(t *testing.T) {
	t.Parallel()

	g := NewGraph()
	input := node.NewUserInputNode(node.WithID("start"))
	if err := g.AddNode(input); err != nil {
		t.Fatalf("add node: %v", err)
	}
	if err := g.SetEntryPoint("start"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddEdge("start", EndNodeRef); err != nil {
		t.Fatalf("add edge: %v", err)
	}

	runner, err := NewRunner(g)
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	if runner.ExecutionStore() == nil {
		t.Fatal("expected default execution store")
	}
	if runner.CheckpointStore() == nil {
		t.Fatal("expected default checkpoint store")
	}
	if runner.EventSink() == nil {
		t.Fatal("expected default event sink")
	}
	if runner.ArtifactStore() == nil {
		t.Fatal("expected default artifact store")
	}

	run, finalState, err := runner.Start(context.Background(), state.FromShared(map[string]any{
		"request": map[string]any{"input": "ready"},
	}))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if run.Status != runtime.RunStatusCompleted {
		t.Fatalf("run status = %q, want completed", run.Status)
	}
	value, ok := state.NewAccess(finalState).ReadAny(state.Shared("request", "input"))
	if !ok || value != "ready" {
		t.Fatalf("final request input = %#v, present=%v", value, ok)
	}
	if _, err := runner.DeleteRun(context.Background(), run.RunID); err != nil {
		t.Fatalf("delete run: %v", err)
	}
	if _, err := runner.GetRun(context.Background(), run.RunID); !errors.Is(err, runtime.ErrRunnerRecordNotFound) {
		t.Fatalf("get deleted run error = %v", err)
	}
}
