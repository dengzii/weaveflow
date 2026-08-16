package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/state"
)

func TestFileRuntimeStoresRejectUnsafeRecordIDs(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	unsafeID := "../outside"
	executionStore := NewFileExecutionStore(filepath.Join(baseDir, "execution"))
	checkpointStore := NewFileCheckpointStore(filepath.Join(baseDir, "checkpoints"))
	artifactStore := NewFileArtifactStore(filepath.Join(baseDir, "artifacts"))
	eventSink := NewFileEventSink(filepath.Join(baseDir, "events"))

	operations := []struct {
		name string
		run  func() error
	}{
		{name: "create run", run: func() error {
			return executionStore.CreateRun(context.Background(), RunRecord{RunID: unsafeID})
		}},
		{name: "save checkpoint", run: func() error {
			return checkpointStore.Save(context.Background(), CheckpointRecord{RunID: unsafeID, CheckpointID: "checkpoint"}, nil)
		}},
		{name: "save artifact", run: func() error {
			_, err := artifactStore.Save(context.Background(), Artifact{RunID: unsafeID, ID: "artifact"})
			return err
		}},
		{name: "save artifact with padded run ID", run: func() error {
			_, err := artifactStore.Save(context.Background(), Artifact{RunID: " run", ID: "artifact"})
			return err
		}},
		{name: "save artifact with padded artifact ID", run: func() error {
			_, err := artifactStore.Save(context.Background(), Artifact{RunID: "run", ID: "artifact "})
			return err
		}},
		{name: "publish event", run: func() error {
			return eventSink.Publish(context.Background(), Event{RunID: unsafeID, Type: EventRunCreated})
		}},
		{name: "create reserved run", run: func() error {
			return executionStore.CreateRun(context.Background(), RunRecord{RunID: fileRunDeletionDirName})
		}},
		{name: "save checkpoint for reserved run", run: func() error {
			return checkpointStore.Save(context.Background(), CheckpointRecord{RunID: fileRunDeletionDirName, CheckpointID: "checkpoint"}, nil)
		}},
		{name: "save artifact for reserved run", run: func() error {
			_, err := artifactStore.Save(context.Background(), Artifact{RunID: fileRunDeletionDirName, ID: "artifact"})
			return err
		}},
		{name: "publish event for reserved run", run: func() error {
			return eventSink.Publish(context.Background(), Event{RunID: fileRunDeletionDirName, Type: EventRunCreated})
		}},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.run(); err == nil {
				t.Fatal("error = nil, want unsafe ID rejection")
			}
		})
	}

	victimDir := filepath.Join(baseDir, "victim")
	if err := os.MkdirAll(victimDir, 0o755); err != nil {
		t.Fatalf("create victim directory: %v", err)
	}
	markerPath := filepath.Join(victimDir, "marker")
	if err := os.WriteFile(markerPath, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write victim marker: %v", err)
	}
	if err := checkpointStore.DeleteRun(context.Background(), "../victim"); err == nil {
		t.Fatal("DeleteRun() error = nil, want unsafe ID rejection")
	}
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("victim marker changed: %v", err)
	}
}

func TestFileRuntimeStoresHonorCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := t.TempDir()

	if _, err := NewFileExecutionStore(dir).GetRun(ctx, "run"); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetRun() error = %v, want context canceled", err)
	}
	if _, err := NewFileCheckpointStore(dir).List(ctx, "run"); !errors.Is(err, context.Canceled) {
		t.Fatalf("checkpoint List() error = %v, want context canceled", err)
	}
	if _, err := NewFileArtifactStore(dir).List(ctx, "run"); !errors.Is(err, context.Canceled) {
		t.Fatalf("artifact List() error = %v, want context canceled", err)
	}
	if err := NewFileEventSink(dir).Publish(ctx, Event{RunID: "run", Type: EventRunStarted}); !errors.Is(err, context.Canceled) {
		t.Fatalf("event Publish() error = %v, want context canceled", err)
	}
}

func TestFileRuntimeStoresDerivePayloadPathsFromRecordIdentity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := t.TempDir()
	outsidePath := filepath.Join(baseDir, "outside-payload")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0o600); err != nil {
		t.Fatalf("write outside payload: %v", err)
	}

	checkpointStore := NewFileCheckpointStore(filepath.Join(baseDir, "checkpoints"))
	checkpoint := CheckpointRecord{RunID: "run", CheckpointID: "checkpoint"}
	if err := checkpointStore.Save(ctx, checkpoint, []byte("checkpoint payload")); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
	var storedCheckpoint CheckpointRecord
	checkpointMetadataPath := checkpointStore.metadataPath(checkpoint.RunID, checkpoint.CheckpointID)
	if err := readRunnerJSONFile(checkpointMetadataPath, &storedCheckpoint); err != nil {
		t.Fatalf("read checkpoint metadata: %v", err)
	}
	storedCheckpoint.PayloadRef = outsidePath
	if err := writeRunnerJSONFile(checkpointMetadataPath, storedCheckpoint); err != nil {
		t.Fatalf("tamper checkpoint metadata: %v", err)
	}
	loadedCheckpoint, checkpointPayload, err := checkpointStore.Load(ctx, checkpoint.CheckpointID)
	if err != nil {
		t.Fatalf("load checkpoint: %v", err)
	}
	if string(checkpointPayload) != "checkpoint payload" || loadedCheckpoint.PayloadRef != checkpointStore.payloadPath(checkpoint.RunID, checkpoint.CheckpointID) {
		t.Fatalf("checkpoint load used persisted payload path: record=%#v payload=%q", loadedCheckpoint, checkpointPayload)
	}

	artifactStore := NewFileArtifactStore(filepath.Join(baseDir, "artifacts"))
	artifactRef, err := artifactStore.Save(ctx, Artifact{RunID: "run", ID: "artifact", Data: []byte("artifact payload")})
	if err != nil {
		t.Fatalf("save artifact: %v", err)
	}
	var storedArtifact state.ArtifactRef
	artifactMetadataPath := artifactStore.metadataPath(artifactRef.RunID, artifactRef.ID)
	if err := readRunnerJSONFile(artifactMetadataPath, &storedArtifact); err != nil {
		t.Fatalf("read artifact metadata: %v", err)
	}
	storedArtifact.Location = outsidePath
	if err := writeRunnerJSONFile(artifactMetadataPath, storedArtifact); err != nil {
		t.Fatalf("tamper artifact metadata: %v", err)
	}
	loadedArtifact, err := artifactStore.Load(ctx, artifactRef)
	if err != nil {
		t.Fatalf("load artifact: %v", err)
	}
	if string(loadedArtifact.Data) != "artifact payload" || loadedArtifact.Location != artifactStore.payloadPath(artifactRef.RunID, artifactRef.ID) {
		t.Fatalf("artifact load used persisted payload path: artifact=%#v", loadedArtifact)
	}
}

func TestFilePayloadStoresUseDistinctNamespaces(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := t.TempDir()
	checkpointStore := NewFileCheckpointStore(baseDir)
	artifactStore := NewFileArtifactStore(baseDir)
	checkpoint := CheckpointRecord{RunID: "shared-run", CheckpointID: "shared-record"}
	artifact := Artifact{RunID: checkpoint.RunID, ID: checkpoint.CheckpointID, Data: []byte("artifact payload")}

	if err := checkpointStore.Save(ctx, checkpoint, []byte("checkpoint payload")); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
	artifactRef, err := artifactStore.Save(ctx, artifact)
	if err != nil {
		t.Fatalf("save artifact: %v", err)
	}
	if checkpointStore.baseDir == artifactStore.baseDir {
		t.Fatalf("checkpoint and artifact stores share base directory %q", checkpointStore.baseDir)
	}
	if checkpointStore.metadataPath(checkpoint.RunID, checkpoint.CheckpointID) == artifactStore.metadataPath(artifactRef.RunID, artifactRef.ID) {
		t.Fatal("checkpoint and artifact metadata paths collide")
	}

	_, checkpointPayload, err := checkpointStore.Load(ctx, checkpoint.CheckpointID)
	if err != nil {
		t.Fatalf("load checkpoint: %v", err)
	}
	if string(checkpointPayload) != "checkpoint payload" {
		t.Fatalf("checkpoint payload = %q, want checkpoint payload", checkpointPayload)
	}
	loadedArtifact, err := artifactStore.Load(ctx, artifactRef)
	if err != nil {
		t.Fatalf("load artifact: %v", err)
	}
	if string(loadedArtifact.Data) != "artifact payload" {
		t.Fatalf("artifact payload = %q, want artifact payload", loadedArtifact.Data)
	}
}

func TestNoopRuntimeStoresReportMissingRecords(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	if _, err := NewNoopExecutionStore().GetRun(ctx, "run"); !errors.Is(err, ErrRunnerRecordNotFound) {
		t.Fatalf("GetRun() error = %v, want record not found", err)
	}
	if _, err := NewNoopExecutionStore().GetStep(ctx, "step"); !errors.Is(err, ErrRunnerRecordNotFound) {
		t.Fatalf("GetStep() error = %v, want record not found", err)
	}
	if _, _, err := NewNoopCheckpointStore().Load(ctx, "checkpoint"); !errors.Is(err, ErrRunnerRecordNotFound) {
		t.Fatalf("Load checkpoint error = %v, want record not found", err)
	}
	if _, err := NewNoopArtifactStore().Load(ctx, state.ArtifactRef{RunID: "run", ID: "artifact"}); !errors.Is(err, ErrRunnerRecordNotFound) {
		t.Fatalf("Load artifact error = %v, want record not found", err)
	}
}

func TestNoopArtifactStoreValidatesDeletionInputs(t *testing.T) {
	t.Parallel()

	store := NewNoopArtifactStore()
	if err := store.DeleteRun(context.Background(), "run"); err != nil {
		t.Fatalf("DeleteRun() error = %v", err)
	}
	if err := store.FenceRunDeletion(context.Background(), "run", "deletion"); err != nil {
		t.Fatalf("FenceRunDeletion() error = %v", err)
	}
	if err := store.DeleteRun(context.Background(), "../run"); err == nil {
		t.Fatal("DeleteRun() error = nil, want invalid run ID rejection")
	}
	if err := store.FenceRunDeletion(context.Background(), "run", "../deletion"); err == nil {
		t.Fatal("FenceRunDeletion() error = nil, want invalid deletion ID rejection")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.DeleteRun(ctx, "run"); !errors.Is(err, context.Canceled) {
		t.Fatalf("DeleteRun() error = %v, want context canceled", err)
	}
	if err := store.FenceRunDeletion(ctx, "run", "deletion"); !errors.Is(err, context.Canceled) {
		t.Fatalf("FenceRunDeletion() error = %v, want context canceled", err)
	}
}

func TestFileStoreInstancesShareDirectoryMutex(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	first := NewFileExecutionStore(baseDir)
	second := NewFileExecutionStore(filepath.Join(baseDir, "."))
	if first.mu.shared != second.mu.shared {
		t.Fatal("stores for the same directory use different mutexes")
	}
}

func TestFilePayloadStoresCommitMetadataAfterPayload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	t.Run("checkpoint", func(t *testing.T) {
		store := NewFileCheckpointStore(t.TempDir())
		record := CheckpointRecord{RunID: "run", CheckpointID: "checkpoint"}
		if err := os.MkdirAll(store.payloadPath(record.RunID, record.CheckpointID), 0o755); err != nil {
			t.Fatalf("block checkpoint payload path: %v", err)
		}
		if err := store.Save(ctx, record, []byte("payload")); err == nil {
			t.Fatal("Save() error = nil, want payload write failure")
		}
		if _, err := os.Stat(store.metadataPath(record.RunID, record.CheckpointID)); !os.IsNotExist(err) {
			t.Fatalf("checkpoint metadata exists after payload failure: %v", err)
		}
		items, err := store.List(ctx, record.RunID)
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(items) != 0 {
			t.Fatalf("List() = %#v, want no visible checkpoint", items)
		}
	})

	t.Run("artifact", func(t *testing.T) {
		store := NewFileArtifactStore(t.TempDir())
		artifact := Artifact{RunID: "run", ID: "artifact", Data: []byte("payload")}
		if err := os.MkdirAll(store.payloadPath(artifact.RunID, artifact.ID), 0o755); err != nil {
			t.Fatalf("block artifact payload path: %v", err)
		}
		if _, err := store.Save(ctx, artifact); err == nil {
			t.Fatal("Save() error = nil, want payload write failure")
		}
		if _, err := os.Stat(store.metadataPath(artifact.RunID, artifact.ID)); !os.IsNotExist(err) {
			t.Fatalf("artifact metadata exists after payload failure: %v", err)
		}
		items, err := store.List(ctx, artifact.RunID)
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(items) != 0 {
			t.Fatalf("List() = %#v, want no visible artifact", items)
		}
	})
}

func TestFilePayloadStoresRejectDuplicateRecordIDs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	t.Run("checkpoint", func(t *testing.T) {
		store := NewFileCheckpointStore(t.TempDir())
		record := CheckpointRecord{RunID: "run", CheckpointID: "checkpoint"}
		if err := store.Save(ctx, record, []byte("original")); err != nil {
			t.Fatalf("save original checkpoint: %v", err)
		}
		if err := store.Save(ctx, record, []byte("replacement")); err == nil {
			t.Fatal("duplicate Save() error = nil")
		}
		_, payload, err := store.Load(ctx, record.CheckpointID)
		if err != nil {
			t.Fatalf("load original checkpoint: %v", err)
		}
		if string(payload) != "original" {
			t.Fatalf("checkpoint payload = %q, want original", payload)
		}

		if err := store.Save(ctx, CheckpointRecord{RunID: "other-run", CheckpointID: record.CheckpointID}, []byte("other")); err != nil {
			t.Fatalf("save duplicate checkpoint ID in another run: %v", err)
		}
		if _, _, err := store.Load(ctx, record.CheckpointID); err == nil || !strings.Contains(err.Error(), "ambiguous") {
			t.Fatalf("ambiguous checkpoint load error = %v", err)
		}
	})

	t.Run("artifact", func(t *testing.T) {
		store := NewFileArtifactStore(t.TempDir())
		artifact := Artifact{RunID: "run", ID: "artifact", Data: []byte("original")}
		ref, err := store.Save(ctx, artifact)
		if err != nil {
			t.Fatalf("save original artifact: %v", err)
		}
		artifact.Data = []byte("replacement")
		if _, err := store.Save(ctx, artifact); err == nil {
			t.Fatal("duplicate Save() error = nil")
		}
		stored, err := store.Load(ctx, ref)
		if err != nil {
			t.Fatalf("load original artifact: %v", err)
		}
		if string(stored.Data) != "original" {
			t.Fatalf("artifact payload = %q, want original", stored.Data)
		}
	})
}

func TestFileExecutionStoreRejectsAmbiguousStepLookup(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewFileExecutionStore(t.TempDir())
	for _, runID := range []string{"run-a", "run-b"} {
		if err := store.CreateRun(ctx, RunRecord{RunID: runID}); err != nil {
			t.Fatalf("create run %q: %v", runID, err)
		}
		if err := store.AppendStep(ctx, StepRecord{RunID: runID, StepID: "shared-step"}); err != nil {
			t.Fatalf("append step for %q: %v", runID, err)
		}
	}
	if _, err := store.GetStep(ctx, "shared-step"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous step lookup error = %v", err)
	}
}

func TestFileExecutionStoreEnforcesRunDeletionReservation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewFileExecutionStore(t.TempDir())
	run := RunRecord{RunID: "run-delete", RootRunID: "run-delete", Status: RunStatusCompleted}
	deletionID := "deletion-file-execution"
	if err := store.CreateRun(ctx, RunRecord{
		RunID: "invalid-new-run", Deletion: &RunDeletionState{
			ID: deletionID, RootRunID: "invalid-new-run", Phase: RunDeletionReserved,
		},
	}); err == nil {
		t.Fatal("CreateRun() accepted a deletion reservation")
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	step := StepRecord{RunID: run.RunID, StepID: "existing-step"}
	if err := store.AppendStep(ctx, step); err != nil {
		t.Fatalf("AppendStep() before deletion error = %v", err)
	}

	stored, err := store.GetRun(ctx, run.RunID)
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	stored.Deletion = &RunDeletionState{
		ID: deletionID, RootRunID: run.RunID, Phase: RunDeletionReserved,
	}
	deletionCtx := withRunDeletionMutation(ctx, deletionID)
	stored, err = store.CompareAndSwapRun(deletionCtx, stored.Revision, stored)
	if err != nil {
		t.Fatalf("reserve deletion error = %v", err)
	}
	if _, err := store.CompareAndSwapRun(ctx, stored.Revision, stored); !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("unfenced CompareAndSwapRun() error = %v, want control rejection", err)
	}
	if err := store.AppendStep(ctx, StepRecord{RunID: run.RunID, StepID: "late-step"}); !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("AppendStep() error = %v, want control rejection", err)
	}
	if err := store.UpdateStep(ctx, step); !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("UpdateStep() error = %v, want control rejection", err)
	}
	if err := store.FenceRunDeletion(deletionCtx, run.RunID, deletionID); err != nil {
		t.Fatalf("FenceRunDeletion() error = %v", err)
	}
	if _, err := os.Stat(store.deletionPath(run.RunID)); err != nil {
		t.Fatalf("durable deletion fence missing: %v", err)
	}
	if err := store.DeleteRun(ctx, run.RunID); !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("unauthorized DeleteRun() error = %v, want control rejection", err)
	}
	if err := store.DeleteRun(withRunDeletionMutation(ctx, "other-deletion"), run.RunID); !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("mismatched DeleteRun() error = %v, want control rejection", err)
	}
	if err := store.DeleteRun(deletionCtx, run.RunID); err != nil {
		t.Fatalf("authorized DeleteRun() error = %v", err)
	}
	if _, err := store.GetRun(ctx, run.RunID); !errors.Is(err, ErrRunnerRecordNotFound) {
		t.Fatalf("GetRun() after deletion error = %v, want record not found", err)
	}
	if _, err := os.Stat(store.stepPath(run.RunID, step.StepID)); !os.IsNotExist(err) {
		t.Fatalf("step survived run deletion: %v", err)
	}
	if err := NewFileExecutionStore(store.baseDir).CreateRun(ctx, run); !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("CreateRun() after physical deletion error = %v, want control rejection", err)
	}
}

func TestFilePayloadStoresPersistRunDeletionFences(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	runID := "run-fenced"
	deletionID := "deletion-file-payloads"
	deletionCtx := withRunDeletionMutation(ctx, deletionID)

	t.Run("checkpoint", func(t *testing.T) {
		baseDir := filepath.Join(t.TempDir(), "checkpoints")
		store := NewFileCheckpointStore(baseDir)
		record := CheckpointRecord{RunID: runID, CheckpointID: "checkpoint"}
		if err := store.Save(ctx, record, []byte("payload")); err != nil {
			t.Fatalf("Save() before fence error = %v", err)
		}
		if err := store.FenceRunDeletion(deletionCtx, runID, deletionID); err != nil {
			t.Fatalf("FenceRunDeletion() error = %v", err)
		}
		if _, err := os.Stat(store.deletionPath(runID)); err != nil {
			t.Fatalf("durable deletion fence missing: %v", err)
		}

		reopened := NewFileCheckpointStore(baseDir)
		if err := reopened.FenceRunDeletion(deletionCtx, runID, deletionID); err != nil {
			t.Fatalf("idempotent FenceRunDeletion() error = %v", err)
		}
		if err := reopened.Save(ctx, CheckpointRecord{RunID: runID, CheckpointID: "late"}, nil); !errors.Is(err, ErrRunControlNotAllowed) {
			t.Fatalf("Save() after fence error = %v, want control rejection", err)
		}
		if err := reopened.DeleteRun(ctx, runID); !errors.Is(err, ErrRunControlNotAllowed) {
			t.Fatalf("unauthorized DeleteRun() error = %v, want control rejection", err)
		}
		if err := reopened.DeleteRun(deletionCtx, runID); err != nil {
			t.Fatalf("authorized DeleteRun() error = %v", err)
		}
		if _, _, err := reopened.Load(ctx, runID); !errors.Is(err, ErrRunnerRecordNotFound) {
			t.Fatalf("Load() interpreted fence as checkpoint: %v", err)
		}
		if err := reopened.Save(ctx, CheckpointRecord{RunID: runID, CheckpointID: "after-delete"}, nil); !errors.Is(err, ErrRunControlNotAllowed) {
			t.Fatalf("Save() after physical deletion error = %v, want control rejection", err)
		}
	})

	t.Run("event", func(t *testing.T) {
		baseDir := filepath.Join(t.TempDir(), "events")
		store := NewFileEventSink(baseDir)
		if err := store.Publish(ctx, Event{ID: "before", RunID: runID, Type: EventRunStarted}); err != nil {
			t.Fatalf("Publish() before fence error = %v", err)
		}
		if err := store.FenceRunDeletion(deletionCtx, runID, deletionID); err != nil {
			t.Fatalf("FenceRunDeletion() error = %v", err)
		}

		reopened := NewFileEventSink(baseDir)
		if err := reopened.Publish(ctx, Event{ID: "late", RunID: runID, Type: EventRunFailed}); !errors.Is(err, ErrRunControlNotAllowed) {
			t.Fatalf("Publish() after fence error = %v, want control rejection", err)
		}
		otherRunID := "run-not-fenced"
		if err := reopened.PublishBatch(ctx, []Event{
			{ID: "other", RunID: otherRunID, Type: EventRunStarted},
			{ID: "fenced", RunID: runID, Type: EventRunFailed},
		}); !errors.Is(err, ErrRunControlNotAllowed) {
			t.Fatalf("PublishBatch() error = %v, want control rejection", err)
		}
		if events, err := reopened.ListEvents(otherRunID); err != nil || len(events) != 0 {
			t.Fatalf("PublishBatch() partially wrote events: events=%#v error=%v", events, err)
		}
		if err := reopened.DeleteRun(withRunDeletionMutation(ctx, "other-deletion"), runID); !errors.Is(err, ErrRunControlNotAllowed) {
			t.Fatalf("mismatched DeleteRun() error = %v, want control rejection", err)
		}
		if err := reopened.DeleteRun(deletionCtx, runID); err != nil {
			t.Fatalf("authorized DeleteRun() error = %v", err)
		}
		if err := reopened.Publish(ctx, Event{ID: "after-delete", RunID: runID, Type: EventRunStarted}); !errors.Is(err, ErrRunControlNotAllowed) {
			t.Fatalf("Publish() after physical deletion error = %v, want control rejection", err)
		}
	})

	t.Run("artifact", func(t *testing.T) {
		baseDir := filepath.Join(t.TempDir(), "artifacts")
		store := NewFileArtifactStore(baseDir)
		if _, err := store.Save(ctx, Artifact{RunID: runID, ID: "before", Data: []byte("payload")}); err != nil {
			t.Fatalf("Save() before fence error = %v", err)
		}
		if err := store.FenceRunDeletion(deletionCtx, runID, deletionID); err != nil {
			t.Fatalf("FenceRunDeletion() error = %v", err)
		}

		reopened := NewFileArtifactStore(baseDir)
		if _, err := reopened.Save(ctx, Artifact{RunID: runID, ID: "late"}); !errors.Is(err, ErrRunControlNotAllowed) {
			t.Fatalf("Save() after fence error = %v, want control rejection", err)
		}
		if err := reopened.DeleteRun(ctx, runID); !errors.Is(err, ErrRunControlNotAllowed) {
			t.Fatalf("unauthorized DeleteRun() error = %v, want control rejection", err)
		}
		if err := reopened.DeleteRun(deletionCtx, runID); err != nil {
			t.Fatalf("authorized DeleteRun() error = %v", err)
		}
		if _, err := reopened.Save(ctx, Artifact{RunID: runID, ID: "after-delete"}); !errors.Is(err, ErrRunControlNotAllowed) {
			t.Fatalf("Save() after physical deletion error = %v, want control rejection", err)
		}
	})
}

func TestFileEventSinkPublishBatchValidatesBeforeWriting(t *testing.T) {
	t.Parallel()

	sink := NewFileEventSink(t.TempDir())
	runID := "run-batch"
	err := sink.PublishBatch(context.Background(), []Event{
		{ID: "first", RunID: runID, Type: EventRunStarted},
		{ID: "invalid", RunID: runID, Type: EventRunFailed, Payload: json.RawMessage("{")},
	})
	if err == nil {
		t.Fatal("PublishBatch() error = nil, want invalid payload rejection")
	}
	events, listErr := sink.ListEvents(runID)
	if listErr != nil {
		t.Fatalf("ListEvents() error = %v", listErr)
	}
	if len(events) != 0 {
		t.Fatalf("ListEvents() = %#v, want no partially written batch", events)
	}
}

func TestWriteRunnerBinaryFileReplacesExistingFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "record")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatalf("write old file: %v", err)
	}
	if err := writeRunnerBinaryFile(path, []byte("new")); err != nil {
		t.Fatalf("writeRunnerBinaryFile() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read replaced file: %v", err)
	}
	if string(data) != "new" {
		t.Fatalf("replaced file = %q, want new", data)
	}
}

func TestFileExecutionStoreListRunsUsesRunIDAsTimestampTieBreaker(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewFileExecutionStore(t.TempDir())
	for _, runID := range []string{"run-b", "run-a"} {
		if err := store.CreateRun(ctx, RunRecord{RunID: runID}); err != nil {
			t.Fatalf("create run %q: %v", runID, err)
		}
	}
	runs, err := store.ListRuns(ctx, RunFilter{})
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if len(runs) != 2 || runs[0].RunID != "run-a" || runs[1].RunID != "run-b" {
		t.Fatalf("ListRuns() = %#v, want run ID order for equal timestamps", runs)
	}
}

func TestFileExecutionStoreEnforcesCreateAndUpdateBoundaries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewFileExecutionStore(t.TempDir())
	run := RunRecord{RunID: "run"}
	step := StepRecord{RunID: run.RunID, StepID: "step"}

	if _, err := store.CompareAndSwapRun(ctx, run.Revision, run); !errors.Is(err, ErrRunnerRecordNotFound) {
		t.Fatalf("CompareAndSwapRun() error = %v, want record not found", err)
	}
	if err := store.AppendStep(ctx, step); !errors.Is(err, ErrRunnerRecordNotFound) {
		t.Fatalf("AppendStep() without run error = %v, want record not found", err)
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	if err := store.CreateRun(ctx, run); err == nil {
		t.Fatal("duplicate CreateRun() error = nil")
	}
	if err := store.UpdateStep(ctx, step); !errors.Is(err, ErrRunnerRecordNotFound) {
		t.Fatalf("UpdateStep() error = %v, want record not found", err)
	}
	if err := store.AppendStep(ctx, step); err != nil {
		t.Fatalf("AppendStep() error = %v", err)
	}
	if err := store.AppendStep(ctx, step); err == nil {
		t.Fatal("duplicate AppendStep() error = nil")
	}
	if _, err := store.CompareAndSwapRun(ctx, run.Revision, run); err != nil {
		t.Fatalf("CompareAndSwapRun() existing record error = %v", err)
	}
	if err := store.UpdateStep(ctx, step); err != nil {
		t.Fatalf("UpdateStep() existing record error = %v", err)
	}
}

func TestFileEventSinkListEventsSupportsLargePayloads(t *testing.T) {
	t.Parallel()

	sink := NewFileEventSink(t.TempDir())
	runID := "run-large-payload"
	largeText := strings.Repeat("x", 256*1024)
	payload, err := json.Marshal(map[string]string{"text": largeText})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	event := Event{
		RunID:   runID,
		Type:    EventLLMReasoning,
		Payload: payload,
	}
	if err := sink.Publish(context.Background(), event); err != nil {
		t.Fatalf("publish event: %v", err)
	}

	events, err := sink.ListEvents(runID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if string(events[0].Payload) != string(payload) {
		t.Fatalf("payload mismatch after reload")
	}

	page, err := sink.ListEventPage(runID, "", 1)
	if err != nil {
		t.Fatalf("list event page: %v", err)
	}
	if len(page.Items) != 1 || string(page.Items[0].Payload) != string(payload) {
		t.Fatal("large payload mismatch after paginated reload")
	}
	if page.NextCursor != "" {
		t.Fatalf("next cursor = %q, want empty", page.NextCursor)
	}
}

func TestFileEventSinkListEventPageReadsNewestFirstAcrossPages(t *testing.T) {
	t.Parallel()

	sink := NewFileEventSink(t.TempDir())
	runID := "run-paginated"
	for index := 0; index < 5; index++ {
		if err := sink.Publish(context.Background(), Event{
			ID:    string(rune('a' + index)),
			RunID: runID,
			Type:  EventRunStarted,
		}); err != nil {
			t.Fatalf("publish event %d: %v", index, err)
		}
	}

	first, err := sink.ListEventPage(runID, "", 2)
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	assertEventIDs(t, first.Items, "e", "d")
	if first.NextCursor == "" {
		t.Fatal("first page next cursor is empty")
	}

	second, err := sink.ListEventPage(runID, first.NextCursor, 2)
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	assertEventIDs(t, second.Items, "c", "b")
	if second.NextCursor == "" {
		t.Fatal("second page next cursor is empty")
	}

	third, err := sink.ListEventPage(runID, second.NextCursor, 2)
	if err != nil {
		t.Fatalf("list third page: %v", err)
	}
	assertEventIDs(t, third.Items, "a")
	if third.NextCursor != "" {
		t.Fatalf("third page next cursor = %q, want empty", third.NextCursor)
	}
}

func TestFileEventSinkListEventPageRejectsInvalidCursor(t *testing.T) {
	t.Parallel()

	sink := NewFileEventSink(t.TempDir())
	if err := sink.Publish(context.Background(), Event{ID: "event-1", RunID: "run-1", Type: EventRunStarted}); err != nil {
		t.Fatalf("publish event: %v", err)
	}
	if _, err := sink.ListEventPage("run-1", "1", 10); !errors.Is(err, ErrInvalidEventCursor) {
		t.Fatalf("ListEventPage() error = %v, want ErrInvalidEventCursor", err)
	}
}

func TestFileEventSinkListEventPageReturnsEmptyPageForMissingRun(t *testing.T) {
	t.Parallel()

	page, err := NewFileEventSink(t.TempDir()).ListEventPage("missing", "", 10)
	if err != nil {
		t.Fatalf("ListEventPage() error = %v", err)
	}
	if len(page.Items) != 0 || page.NextCursor != "" {
		t.Fatalf("page = %#v, want empty", page)
	}
}

func assertEventIDs(t *testing.T, events []Event, want ...string) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("event count = %d, want %d; events = %#v", len(events), len(want), events)
	}
	for index, event := range events {
		if event.ID != want[index] {
			t.Fatalf("event %d id = %q, want %q", index, event.ID, want[index])
		}
	}
}
