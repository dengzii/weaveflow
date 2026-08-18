package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/state"
)

func TestMemoryStoresRoundTripCloneAndDeleteRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	executionStore := NewMemoryExecutionStore()
	checkpointStore := NewMemoryCheckpointStore()
	eventSink := NewMemoryEventSink()
	artifactStore := NewMemoryArtifactStore()
	startedAt := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	run := RunRecord{RunID: "run-1", RootRunID: "run-1", Status: RunStatusCompleted, CurrentNodeIDs: []string{"start"}, StartedAt: startedAt, UpdatedAt: startedAt}
	if err := executionStore.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	run.CurrentNodeIDs[0] = "changed"
	storedRun, err := executionStore.GetRun(ctx, "run-1")
	if err != nil || storedRun.CurrentNodeIDs[0] != "start" {
		t.Fatalf("GetRun() = %#v, %v", storedRun, err)
	}

	step := StepRecord{StepID: "step-1", RunID: "run-1", NodeID: "start", Status: StepStatusRunning, StartedAt: startedAt, UpdatedAt: startedAt}
	if err := executionStore.AppendStep(ctx, step); err != nil {
		t.Fatalf("AppendStep() error = %v", err)
	}
	payload := []byte("checkpoint")
	checkpoint := CheckpointRecord{CheckpointID: "checkpoint-1", RunID: "run-1", StepID: "step-1", NodeID: "start", Stage: CheckpointAfterNode, CreatedAt: startedAt}
	if err := checkpointStore.Save(ctx, checkpoint, payload); err != nil {
		t.Fatalf("Save() checkpoint error = %v", err)
	}
	payload[0] = 'X'
	_, storedPayload, err := checkpointStore.Load(ctx, "checkpoint-1")
	if err != nil || string(storedPayload) != "checkpoint" {
		t.Fatalf("Load() checkpoint payload = %q, %v", storedPayload, err)
	}

	event := Event{ID: "event-1", RunID: "run-1", Type: EventRunStarted, Timestamp: startedAt, Payload: []byte(`{"status":"running"}`)}
	if err := eventSink.Publish(ctx, event); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	event.Payload[0] = 'X'
	events, err := eventSink.ListEvents("run-1")
	if err != nil || len(events) != 1 || string(events[0].Payload) != `{"status":"running"}` {
		t.Fatalf("ListEvents() = %#v, %v", events, err)
	}

	artifactData := []byte("artifact")
	ref, err := stageAndFinalizeTestArtifact(ctx, artifactStore, "artifact-transaction-1", Artifact{ID: "artifact-1", RunID: "run-1", Type: "text", Data: artifactData})
	if err != nil {
		t.Fatalf("Save() artifact error = %v", err)
	}
	artifactData[0] = 'X'
	storedArtifact, err := artifactStore.Load(ctx, ref)
	if err != nil || string(storedArtifact.Data) != "artifact" {
		t.Fatalf("Load() artifact = %#v, %v", storedArtifact, err)
	}

	deleter := NewRunDeletionCoordinator(executionStore, checkpointStore, eventSink, artifactStore)
	if err := deleter.DeleteRun(ctx, "run-1"); err != nil {
		t.Fatalf("DeleteRun() error = %v", err)
	}
	if _, err := executionStore.GetRun(ctx, "run-1"); !errors.Is(err, ErrRunnerRecordNotFound) {
		t.Fatalf("GetRun() after delete error = %v", err)
	}
	if checkpoints, err := checkpointStore.List(ctx, "run-1"); err != nil || len(checkpoints) != 0 {
		t.Fatalf("List() checkpoints after delete = %#v, %v", checkpoints, err)
	}
	if events, err := eventSink.ListEvents("run-1"); err != nil || len(events) != 0 {
		t.Fatalf("ListEvents() after delete = %#v, %v", events, err)
	}
	if artifacts, err := artifactStore.List(ctx, "run-1"); err != nil || len(artifacts) != 0 {
		t.Fatalf("List() artifacts after delete = %#v, %v", artifacts, err)
	}
}

func TestMemoryExecutionStoreEnforcesRunDeletion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryExecutionStore()

	invalid := RunRecord{
		RunID:  "run-invalid-deletion",
		Status: RunStatusCompleted,
		Deletion: &RunDeletionState{
			ID: "deletion-invalid", RootRunID: "run-invalid-deletion", Phase: RunDeletionReserved,
		},
	}
	if err := store.CreateRun(ctx, invalid); err == nil {
		t.Fatal("CreateRun() accepted a new run with deletion state")
	}

	run := RunRecord{RunID: "run-deletion", RootRunID: "run-deletion", Status: RunStatusCompleted}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	step := StepRecord{RunID: run.RunID, StepID: "step-before-deletion", Status: StepStatusSucceeded}
	if err := store.AppendStep(ctx, step); err != nil {
		t.Fatalf("AppendStep() before deletion error = %v", err)
	}

	stored, err := store.GetRun(ctx, run.RunID)
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	const deletionID = "deletion-1"
	stored.Deletion = &RunDeletionState{ID: deletionID, RootRunID: run.RunID, Phase: RunDeletionReserved}
	stored, err = store.CompareAndSwapRun(withRunDeletionMutation(ctx, deletionID), stored.Revision, stored)
	if err != nil {
		t.Fatalf("CompareAndSwapRun() reserve deletion error = %v", err)
	}
	deletionCtx := withRunDeletionMutation(ctx, deletionID)
	if err := store.FenceRunDeletion(ctx, run.RunID, deletionID); !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("FenceRunDeletion() without mutation error = %v, want control not allowed", err)
	}
	if err := store.FenceRunDeletion(deletionCtx, run.RunID, deletionID); err != nil {
		t.Fatalf("FenceRunDeletion() error = %v", err)
	}
	if err := store.FenceRunDeletion(deletionCtx, run.RunID, deletionID); err != nil {
		t.Fatalf("FenceRunDeletion() idempotent error = %v", err)
	}
	if err := store.FenceRunDeletion(withRunDeletionMutation(ctx, "other-deletion"), run.RunID, "other-deletion"); !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("FenceRunDeletion() conflict error = %v, want control not allowed", err)
	}

	if _, err := store.CompareAndSwapRun(ctx, stored.Revision, stored); !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("CompareAndSwapRun() after deletion error = %v, want control not allowed", err)
	}
	if err := store.AppendStep(ctx, StepRecord{RunID: run.RunID, StepID: "step-after-deletion"}); !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("AppendStep() after deletion error = %v, want control not allowed", err)
	}
	step.Status = StepStatusFailed
	if err := store.UpdateStep(ctx, step); !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("UpdateStep() after deletion error = %v, want control not allowed", err)
	}

	if err := store.DeleteRun(withRunDeletionMutation(ctx, "other-deletion"), run.RunID); !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("DeleteRun() with wrong deletion ID error = %v, want control not allowed", err)
	}
	if _, err := store.GetRun(ctx, run.RunID); err != nil {
		t.Fatalf("GetRun() after rejected deletion error = %v", err)
	}
	if err := store.DeleteRun(withRunDeletionMutation(ctx, deletionID), run.RunID); err != nil {
		t.Fatalf("DeleteRun() with matching deletion ID error = %v", err)
	}
	if _, err := store.GetRun(ctx, run.RunID); !errors.Is(err, ErrRunnerRecordNotFound) {
		t.Fatalf("GetRun() after deletion error = %v, want record not found", err)
	}
	if _, err := store.GetStep(ctx, step.StepID); !errors.Is(err, ErrRunnerRecordNotFound) {
		t.Fatalf("GetStep() after deletion error = %v, want record not found", err)
	}
	if err := store.DeleteRun(withRunDeletionMutation(ctx, "other-deletion"), run.RunID); !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("DeleteRun() missing run with wrong deletion ID error = %v, want control not allowed", err)
	}
	if err := store.DeleteRun(deletionCtx, run.RunID); err != nil {
		t.Fatalf("DeleteRun() idempotent error = %v", err)
	}
	if err := store.CreateRun(ctx, run); !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("CreateRun() after deletion error = %v, want control not allowed", err)
	}
}

func TestMemoryRunDataStoresEnforceDeletionFence(t *testing.T) {
	t.Parallel()

	t.Run("checkpoint", func(t *testing.T) {
		store := NewMemoryCheckpointStore()
		assertMemoryRunDeletionFence(t,
			store.FenceRunDeletion,
			func(ctx context.Context) error {
				return store.Save(ctx, CheckpointRecord{RunID: "run-fenced", CheckpointID: "checkpoint-before"}, []byte("before"))
			},
			func(ctx context.Context) error {
				return store.Save(ctx, CheckpointRecord{RunID: "run-fenced", CheckpointID: "checkpoint-after"}, []byte("after"))
			},
			store.DeleteRun,
			func() (int, error) {
				items, err := store.List(context.Background(), "run-fenced")
				return len(items), err
			},
		)
	})

	t.Run("event", func(t *testing.T) {
		store := NewMemoryEventSink()
		assertMemoryRunDeletionFence(t,
			store.FenceRunDeletion,
			func(ctx context.Context) error {
				return store.Publish(ctx, Event{ID: "event-before", RunID: "run-fenced", Type: EventRunFinished})
			},
			func(ctx context.Context) error {
				return store.Publish(ctx, Event{ID: "event-after", RunID: "run-fenced", Type: EventRunFinished})
			},
			store.DeleteRun,
			func() (int, error) {
				items, err := store.ListEvents("run-fenced")
				return len(items), err
			},
		)
	})

	t.Run("artifact", func(t *testing.T) {
		store := NewMemoryArtifactStore()
		assertMemoryRunDeletionFence(t,
			store.FenceRunDeletion,
			func(ctx context.Context) error {
				_, err := stageAndFinalizeTestArtifact(ctx, store, "artifact-before-transaction", Artifact{ID: "artifact-before", RunID: "run-fenced", Data: []byte("before")})
				return err
			},
			func(ctx context.Context) error {
				_, err := stageAndFinalizeTestArtifact(ctx, store, "artifact-after-transaction", Artifact{ID: "artifact-after", RunID: "run-fenced", Data: []byte("after")})
				return err
			},
			store.DeleteRun,
			func() (int, error) {
				items, err := store.List(context.Background(), "run-fenced")
				return len(items), err
			},
		)
	})
}

func stageAndFinalizeTestArtifact(ctx context.Context, store ArtifactStore, transactionID string, artifact Artifact) (state.ArtifactRef, error) {
	stage, err := store.Stage(ctx, transactionID, artifact)
	if err != nil {
		return state.ArtifactRef{}, err
	}
	if err := store.Finalize(ctx, transactionID, []ArtifactStage{stage}); err != nil {
		return state.ArtifactRef{}, err
	}
	return stage.Ref, nil
}

func assertMemoryRunDeletionFence(
	t *testing.T,
	fence func(context.Context, string, string) error,
	initialWrite func(context.Context) error,
	lateWrite func(context.Context) error,
	deleteRun func(context.Context, string) error,
	count func() (int, error),
) {
	t.Helper()
	ctx := context.Background()
	const runID = "run-fenced"
	const deletionID = "deletion-fence"

	if err := initialWrite(ctx); err != nil {
		t.Fatalf("initial write error = %v", err)
	}
	deletionCtx := withRunDeletionMutation(ctx, deletionID)
	if err := fence(ctx, runID, deletionID); !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("FenceRunDeletion() without mutation error = %v, want control not allowed", err)
	}
	if err := fence(deletionCtx, runID, deletionID); err != nil {
		t.Fatalf("FenceRunDeletion() error = %v", err)
	}
	if err := fence(deletionCtx, runID, deletionID); err != nil {
		t.Fatalf("FenceRunDeletion() idempotent error = %v", err)
	}
	if err := fence(withRunDeletionMutation(ctx, "other-deletion"), runID, "other-deletion"); !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("FenceRunDeletion() conflict error = %v, want control not allowed", err)
	}
	if err := lateWrite(ctx); !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("late write error = %v, want control not allowed", err)
	}
	if err := deleteRun(withRunDeletionMutation(ctx, "other-deletion"), runID); !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("DeleteRun() with wrong deletion ID error = %v, want control not allowed", err)
	}
	if itemCount, err := count(); err != nil || itemCount != 1 {
		t.Fatalf("count after rejected deletion = %d, %v, want 1", itemCount, err)
	}
	if err := deleteRun(withRunDeletionMutation(ctx, deletionID), runID); err != nil {
		t.Fatalf("DeleteRun() error = %v", err)
	}
	if itemCount, err := count(); err != nil || itemCount != 0 {
		t.Fatalf("count after deletion = %d, %v, want 0", itemCount, err)
	}
	if err := deleteRun(withRunDeletionMutation(ctx, "other-deletion"), runID); !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("DeleteRun() after deletion with wrong deletion ID error = %v, want control not allowed", err)
	}
	if err := deleteRun(deletionCtx, runID); err != nil {
		t.Fatalf("DeleteRun() idempotent error = %v", err)
	}
	if err := lateWrite(ctx); !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("write after deletion error = %v, want control not allowed", err)
	}
}
