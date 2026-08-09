package graph

import (
	"testing"

	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

func mustNewGraphRunner(t *testing.T, graph *Graph, executionStore fruntime.ExecutionStore, checkpointStore fruntime.CheckpointStore, codec state.StateCodec, eventSink fruntime.EventSink, options ...fruntime.GraphRunnerOption) *fruntime.GraphRunner {
	t.Helper()
	runner, err := NewGraphRunner(graph, executionStore, checkpointStore, codec, eventSink, options...)
	if err != nil {
		t.Fatalf("NewGraphRunner() error: %v", err)
	}
	return runner
}
