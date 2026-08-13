package graph

import (
	"testing"

	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

func mustNewGraphRunner(t *testing.T, testGraph *Graph, executionStore fruntime.ExecutionStore, checkpointStore fruntime.CheckpointStore, codec state.Codec, eventSink fruntime.EventSink, options ...fruntime.GraphRunnerOption) *fruntime.GraphRunner {
	t.Helper()
	runner, err := NewGraphRunner(testGraph, executionStore, checkpointStore, codec, eventSink, options...)
	if err != nil {
		t.Fatalf("NewGraphRunner() error: %v", err)
	}
	return runner
}
