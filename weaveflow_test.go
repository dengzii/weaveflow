package weaveflow

import (
	"context"
	"testing"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/state"
)

func TestNewRunnerAppliesDefaults(t *testing.T) {
	t.Parallel()

	g := NewGraph()
	fn := node.NewFuncNode(node.Spec{ID: "start"}, func(ctx core.Context, access *state.Access) error {
		return access.SetAny(state.Shared("ok"), true)
	})
	if err := g.AddNode(fn); err != nil {
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
	if runner.ExecutionStore == nil {
		t.Fatal("expected default execution store")
	}
	if runner.CheckpointStore == nil {
		t.Fatal("expected default checkpoint store")
	}
	if runner.Codec == nil {
		t.Fatal("expected default state codec")
	}
	if runner.EventSink == nil {
		t.Fatal("expected default event sink")
	}
	if runner.ArtifactStore == nil {
		t.Fatal("expected default artifact store")
	}

	run, finalState, err := runner.Start(context.Background(), NewState())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if run.Status != RunStatusCompleted {
		t.Fatalf("run status = %q, want completed", run.Status)
	}
	value, ok := state.NewAccess(nil, finalState).ReadAny(state.Shared("ok"))
	if !ok || value != true {
		t.Fatalf("final state ok = %#v, present=%v", value, ok)
	}
}
