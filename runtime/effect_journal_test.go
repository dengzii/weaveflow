package runtime

import (
	"context"
	"testing"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/state"
)

func TestEffectJournalPersistsIntentAndOutcomeWithStableOperationKey(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryRuntimeStore()
	run := RunRecord{RunID: "run-effect-journal", Status: RunStatusRunning}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	runner := &GraphRunner{
		executionStore:   store,
		transactionStore: store,
		eventSink:        store,
		artifactStore:    NewNoopArtifactStore(),
	}
	execution := newGraphRunnerExecution(runner, run, state.NewState(), nil, nil, nil)
	ctx = WithRunnerMetadata(ctx, RunnerMetadata{
		RunID: run.RunID, StepID: "step-effect-journal", TaskID: "task-effect-journal", NodeID: "node-effect-journal", Attempt: 1,
	})
	operation := core.EffectOperation{
		Key: "operation-effect-journal", Kind: "tool", Name: "provider-write", Class: core.EffectIdempotentWrite,
		Status: core.EffectIntent, Attempt: 1, IdempotencyKey: "operation-effect-journal",
	}
	if err := execution.recordEffect(ctx, operation); err != nil {
		t.Fatalf("record intent: %v", err)
	}
	operation.Status = core.EffectSucceeded
	operation.ProviderRequestID = "provider-request-1"
	if err := execution.recordEffect(ctx, operation); err != nil {
		t.Fatalf("record outcome: %v", err)
	}
	events, err := store.ListEvents(run.RunID)
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(events) != 2 || events[0].Type != EventEffectIntent || events[1].Type != EventEffectOutcome {
		t.Fatalf("effect events = %#v", events)
	}
	for _, event := range events {
		if event.OperationKey != operation.Key {
			t.Fatalf("event operation key = %q, want %q", event.OperationKey, operation.Key)
		}
	}
	transactionID := stableRuntimeID("effect", operation.Key, string(operation.Status), "1")
	result, err := store.ResolveCommit(ctx, transactionID)
	if err != nil || result.Outcome != TransactionCommitted {
		t.Fatalf("ResolveCommit() = %#v, %v", result, err)
	}
}
