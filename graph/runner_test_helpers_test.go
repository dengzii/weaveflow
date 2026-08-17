package graph

import (
	"testing"

	filestore "github.com/dengzii/weaveflow/internal/runtimestore/file"
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

func mustOpenFileStore(t *testing.T, directory string) *filestore.Store {
	t.Helper()
	store, err := filestore.Open(directory)
	if err != nil {
		t.Fatalf("file.Open() error: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("file Store.Close() error: %v", err)
		}
	})
	return store
}

func mustNewFileGraphRunner(t *testing.T, testGraph *Graph, directory string, options ...fruntime.GraphRunnerOption) (*fruntime.GraphRunner, *filestore.Store) {
	t.Helper()
	store := mustOpenFileStore(t, directory)
	runner := mustNewGraphRunner(
		t,
		testGraph,
		store.ExecutionStore(),
		store.CheckpointStore(),
		state.NewJSONStateCodec(""),
		store.EventSink(),
		append([]fruntime.GraphRunnerOption{
			fruntime.WithRuntimeTransactionStore(store),
			fruntime.WithStoreCloser(store),
		}, options...)...,
	)
	return runner, store
}
