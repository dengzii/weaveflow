package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/state"
)

func TestCheckpointForkCreatesIndependentRunAndIsIdempotent(t *testing.T) {
	store := NewMemoryRuntimeStore()
	workflow := &blockingChildRunGraph{}
	runner := &GraphRunner{
		graph: workflow, executionStore: store, checkpointStore: store,
		artifactStore: NewNoopArtifactStore(), codec: state.NewJSONStateCodec(""), eventSink: store,
		transactionStore: store, graphID: "graph", graphVersion: "v1", now: time.Now,
		activeExecutions: make(map[string]*graphRunnerExecution),
	}
	initial := state.FromShared(map[string]any{"value": "source"})
	run, _, commit, err := runner.prepareRun(context.Background(), initial, nil, true)
	if err != nil {
		t.Fatalf("prepareRun() = %v", err)
	}
	if _, err := runner.commitRuntime(context.Background(), commit); err != nil {
		t.Fatalf("commit source run = %v", err)
	}
	prepared, err := runner.prepareFork(context.Background(), ForkRequest{
		SourceRunID: run.RunID, SourceCheckpointID: run.LastCheckpointID, RequestKey: "fork-once",
		Input: state.FromShared(map[string]any{"value": "fork"}),
	})
	if err != nil {
		t.Fatalf("prepareFork() = %v", err)
	}
	if !prepared.created || prepared.result.Run.RunID == run.RunID {
		t.Fatalf("fork result = %#v, want independent created run", prepared.result)
	}
	if prepared.result.Run.SourceRunID != run.RunID || prepared.result.Run.SourceCheckpointID != run.LastCheckpointID {
		t.Fatalf("fork provenance = %#v", prepared.result.Run)
	}
	source, err := runner.GetRun(context.Background(), run.RunID)
	if err != nil {
		t.Fatalf("reload source run = %v", err)
	}
	if source.LastCheckpointID != run.LastCheckpointID || source.SourceRunID != "" {
		t.Fatalf("source run changed after fork = %#v", source)
	}
	repeated, err := runner.prepareFork(context.Background(), ForkRequest{
		SourceRunID: run.RunID, SourceCheckpointID: run.LastCheckpointID, RequestKey: "fork-once",
	})
	if err != nil {
		t.Fatalf("repeat prepareFork() = %v", err)
	}
	if repeated.created || repeated.result.Run.RunID != prepared.result.Run.RunID {
		t.Fatalf("repeat fork = %#v, want same existing run", repeated.result)
	}
}

func TestRunCompareReportsStateChanges(t *testing.T) {
	store := NewMemoryRuntimeStore()
	workflow := &blockingChildRunGraph{}
	runner := &GraphRunner{
		graph: workflow, executionStore: store, checkpointStore: store,
		artifactStore: NewNoopArtifactStore(), codec: state.NewJSONStateCodec(""), eventSink: store,
		transactionStore: store, graphID: "graph", graphVersion: "v1", now: time.Now,
		activeExecutions: make(map[string]*graphRunnerExecution),
	}
	initial := state.FromShared(map[string]any{"value": "source"})
	left, _, leftCommit, err := runner.prepareRun(context.Background(), initial, nil, true)
	if err != nil {
		t.Fatalf("prepare left run = %v", err)
	}
	if _, err := runner.commitRuntime(context.Background(), leftCommit); err != nil {
		t.Fatalf("commit left run = %v", err)
	}
	right, _, rightCommit, err := runner.prepareRun(context.Background(), state.FromShared(map[string]any{"value": "right"}), nil, true)
	if err != nil {
		t.Fatalf("prepare right run = %v", err)
	}
	if _, err := runner.commitRuntime(context.Background(), rightCommit); err != nil {
		t.Fatalf("commit right run = %v", err)
	}
	comparison, err := runner.CompareRuns(context.Background(), left.RunID, right.RunID)
	if err != nil {
		t.Fatalf("CompareRuns() = %v", err)
	}
	if len(comparison.StateChanges) == 0 {
		t.Fatal("CompareRuns() returned no state changes")
	}
}
