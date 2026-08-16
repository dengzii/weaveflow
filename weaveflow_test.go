package weaveflow

import (
	"context"
	"errors"
	"strings"
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

func TestNewRunnerRequiresDeletionCapableStores(t *testing.T) {
	t.Parallel()

	executionStore := runtime.NewMemoryExecutionStore()
	checkpointStore := runtime.NewMemoryCheckpointStore()
	eventStore := runtime.NewMemoryEventSink()
	artifactStore := runtime.NewMemoryArtifactStore()
	tests := []struct {
		name   string
		option RunnerOption
		want   string
	}{
		{
			name: "execution deletion",
			option: WithExecutionStore(struct {
				runtime.ExecutionStore
			}{ExecutionStore: executionStore}),
			want: "execution store does not support run deletion",
		},
		{
			name: "execution fencing",
			option: WithExecutionStore(struct {
				runtime.ExecutionStore
				runtime.RunDeleter
			}{ExecutionStore: executionStore, RunDeleter: executionStore}),
			want: "execution store does not support run deletion fencing",
		},
		{
			name: "checkpoint deletion",
			option: WithCheckpointStore(struct {
				runtime.CheckpointStore
			}{CheckpointStore: checkpointStore}),
			want: "checkpoint store does not support run deletion",
		},
		{
			name: "checkpoint fencing",
			option: WithCheckpointStore(struct {
				runtime.CheckpointStore
				runtime.RunDeleter
			}{CheckpointStore: checkpointStore, RunDeleter: checkpointStore}),
			want: "checkpoint store does not support run deletion fencing",
		},
		{
			name: "event deletion",
			option: WithEventSink(struct {
				runtime.EventSink
			}{EventSink: eventStore}),
			want: "event store does not support run deletion",
		},
		{
			name: "event fencing",
			option: WithEventSink(struct {
				runtime.EventSink
				runtime.RunDeleter
			}{EventSink: eventStore, RunDeleter: eventStore}),
			want: "event store does not support run deletion fencing",
		},
		{
			name: "artifact deletion",
			option: WithArtifactStore(struct {
				runtime.ArtifactStore
			}{ArtifactStore: artifactStore}),
			want: "artifact store does not support run deletion",
		},
		{
			name: "artifact fencing",
			option: WithArtifactStore(struct {
				runtime.ArtifactStore
				runtime.RunDeleter
			}{ArtifactStore: artifactStore, RunDeleter: artifactStore}),
			want: "artifact store does not support run deletion fencing",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workflow := NewGraph()
			input := node.NewUserInputNode(node.WithID("start"))
			if err := workflow.AddNode(input); err != nil {
				t.Fatalf("add node: %v", err)
			}
			if err := workflow.SetEntryPoint("start"); err != nil {
				t.Fatalf("set entry: %v", err)
			}
			if err := workflow.AddEdge("start", EndNodeRef); err != nil {
				t.Fatalf("add edge: %v", err)
			}

			runner, err := NewRunner(workflow, test.option)
			if runner != nil || err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NewRunner() = %#v, %v; want %q", runner, err, test.want)
			}
		})
	}
}
