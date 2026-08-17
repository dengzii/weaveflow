package file

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	fruntime "github.com/dengzii/weaveflow/runtime"
)

type interruptingDeletionStore struct {
	deleter  fruntime.RunDeleter
	fencer   fruntime.RunDeletionFencer
	failures int
}

func (store *interruptingDeletionStore) DeleteRun(ctx context.Context, runID string) error {
	if store.failures > 0 {
		store.failures--
		return errors.New("injected deletion interruption")
	}
	return store.deleter.DeleteRun(ctx, runID)
}

func (store *interruptingDeletionStore) FenceRunDeletion(ctx context.Context, runID, deletionID string) error {
	return store.fencer.FenceRunDeletion(ctx, runID, deletionID)
}

func TestFileDeletionManifestReconcilesAfterInterruption(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	store := openTestStore(t, directory)
	if err := store.CreateRun(ctx, RunRecord{RunID: "root", RootRunID: "root", Status: fruntime.RunStatusCompleted}); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	checkpointStore := store.CheckpointStore()
	interrupted := &interruptingDeletionStore{
		deleter:  store.CheckpointDeleter(),
		fencer:   checkpointStore.(fruntime.RunDeletionFencer),
		failures: 1,
	}
	coordinator := fruntime.NewRunDeletionCoordinator(
		store.ExecutionDeletionStore(), interrupted, store.EventDeleter(), store.ArtifactDeleter(),
	)
	if err := coordinator.DeleteRun(ctx, "root"); err == nil || !strings.Contains(err.Error(), "interruption") {
		t.Fatalf("DeleteRun() error = %v, want interruption", err)
	}
	manifests, err := store.ListRunDeletionManifests(ctx)
	if err != nil || len(manifests) != 1 {
		t.Fatalf("ListRunDeletionManifests() = %#v, error = %v", manifests, err)
	}
	if manifests[0].Phase != fruntime.RunDeletionUnlinked {
		t.Fatalf("manifest phase = %q, want unlinked", manifests[0].Phase)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(directory)
	if err != nil {
		t.Fatalf("Open() after interruption error = %v", err)
	}
	defer func() { _ = reopened.Close() }()
	reconciler := fruntime.NewRunDeletionCoordinator(
		reopened.ExecutionDeletionStore(), reopened.CheckpointDeleter(), reopened.EventDeleter(), reopened.ArtifactDeleter(),
	)
	if err := reconciler.ReconcileRunDeletions(ctx); err != nil {
		t.Fatalf("ReconcileRunDeletions() error = %v", err)
	}
	manifest, err := reopened.LoadRunDeletionManifest(ctx, manifests[0].ID)
	if err != nil {
		t.Fatalf("LoadRunDeletionManifest() error = %v", err)
	}
	if manifest.Phase != fruntime.RunDeletionDeleted {
		t.Fatalf("manifest phase after reconciliation = %q, want deleted", manifest.Phase)
	}
	if _, err := reopened.GetRun(ctx, "root"); !errors.Is(err, fruntime.ErrRunnerRecordNotFound) {
		t.Fatalf("GetRun() after reconciliation error = %v, want not found", err)
	}
	if err := reconciler.ReconcileRunDeletions(ctx); err != nil {
		t.Fatalf("second ReconcileRunDeletions() error = %v", err)
	}
}

func TestFileStoreRejectsOrphanDeletionFenceOnOpen(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	store := openTestStore(t, directory)
	if err := store.CreateRun(ctx, RunRecord{RunID: "run", RootRunID: "run", Status: fruntime.RunStatusCompleted}); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	fencer := store.CheckpointStore().(fruntime.RunDeletionFencer)
	if err := fencer.FenceRunDeletion(fruntime.WithRunDeletionMutation(ctx, "deletion"), "run", "deletion"); err != nil {
		t.Fatalf("FenceRunDeletion() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := Open(directory); err == nil || !strings.Contains(err.Error(), "deletion fence") {
		t.Fatalf("Open() error = %v, want orphan deletion fence rejection", err)
	}
}

func TestFileStoreRejectsCorruptDeletionManifestOnOpen(t *testing.T) {
	directory := t.TempDir()
	store := openTestStore(t, directory)
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	manifestDir := filepath.Join(directory, ".deletions", "manifests")
	if err := os.MkdirAll(manifestDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	manifest := filepath.Join(manifestDir, "corrupt.json")
	if err := os.WriteFile(manifest, []byte(`{"id":"corrupt","root_run_id":"root","phase":"unknown","run_ids":["root"],"created_at":"2026-08-17T00:00:00Z","updated_at":"2026-08-17T00:00:00Z"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := Open(directory); err == nil || !strings.Contains(err.Error(), "unsupported phase") {
		t.Fatalf("Open() error = %v, want corrupt manifest rejection", err)
	}
}

func TestFileStoreRejectsPlannedManifestWithoutRoot(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	store := openTestStore(t, directory)
	now := time.Now().UTC()
	manifest := RunDeletionManifest{
		ID: "deletion", RootRunID: "root", Phase: fruntime.RunDeletionPlanned,
		RunIDs: []string{"root"}, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveRunDeletionManifest(ctx, manifest); err != nil {
		t.Fatalf("SaveRunDeletionManifest() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := Open(directory); err == nil || !strings.Contains(err.Error(), "planned root") {
		t.Fatalf("Open() error = %v, want missing planned root rejection", err)
	}
}
