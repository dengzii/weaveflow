package runtime

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingRunDeleter struct {
	name  string
	calls *[]string
	err   error
}

func (store recordingRunDeleter) DeleteRun(_ context.Context, runID string) error {
	*store.calls = append(*store.calls, store.name+":"+runID)
	return store.err
}

func (store recordingRunDeleter) FenceRunDeletion(ctx context.Context, runID, deletionID string) error {
	return requireRunDeletionMutation(ctx, runID, deletionID)
}

type recordingExecutionStore struct {
	*MemoryExecutionStore
	name  string
	calls *[]string
}

type flakyCheckpointDeletionStore struct {
	*MemoryCheckpointStore
	mu       sync.Mutex
	failures int
	fenceIDs []string
}

func (store *flakyCheckpointDeletionStore) FenceRunDeletion(ctx context.Context, runID, deletionID string) error {
	store.mu.Lock()
	store.fenceIDs = append(store.fenceIDs, deletionID)
	store.mu.Unlock()
	return store.MemoryCheckpointStore.FenceRunDeletion(ctx, runID, deletionID)
}

func (store *flakyCheckpointDeletionStore) DeleteRun(ctx context.Context, runID string) error {
	store.mu.Lock()
	if store.failures > 0 {
		store.failures--
		store.mu.Unlock()
		return errors.New("checkpoint deletion interrupted")
	}
	store.mu.Unlock()
	return store.MemoryCheckpointStore.DeleteRun(ctx, runID)
}

type hookedCheckpointDeletionStore struct {
	*MemoryCheckpointStore
	once sync.Once
	hook func(context.Context) error
	err  error
}

type mutatingHierarchyExecutionStore struct {
	*MemoryExecutionStore
	targetRunID string
	once        sync.Once
	hook        func(context.Context) error
	err         error
}

func (store *mutatingHierarchyExecutionStore) GetRun(ctx context.Context, runID string) (RunRecord, error) {
	run, err := store.MemoryExecutionStore.GetRun(ctx, runID)
	if err != nil || runID != store.targetRunID {
		return run, err
	}
	store.once.Do(func() {
		if store.hook != nil {
			store.err = store.hook(ctx)
		}
	})
	if store.err != nil {
		return RunRecord{}, store.err
	}
	return run, nil
}

func (store *hookedCheckpointDeletionStore) FenceRunDeletion(ctx context.Context, runID, deletionID string) error {
	store.once.Do(func() {
		if store.hook != nil {
			store.err = store.hook(ctx)
		}
	})
	if store.err != nil {
		return store.err
	}
	return store.MemoryCheckpointStore.FenceRunDeletion(ctx, runID, deletionID)
}

func (store *recordingExecutionStore) DeleteRun(ctx context.Context, runID string) error {
	*store.calls = append(*store.calls, store.name+":"+runID)
	return store.MemoryExecutionStore.DeleteRun(ctx, runID)
}

func TestRunDeletionCoordinatorDeletesExecutionLast(t *testing.T) {
	var calls []string
	executionStore := &recordingExecutionStore{
		MemoryExecutionStore: NewMemoryExecutionStore(),
		name:                 "execution",
		calls:                &calls,
	}
	if err := executionStore.CreateRun(context.Background(), RunRecord{RunID: "run-1", RootRunID: "run-1", Status: RunStatusCompleted}); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	coordinator := NewRunDeletionCoordinator(
		executionStore,
		recordingRunDeleter{name: "checkpoints", calls: &calls},
		recordingRunDeleter{name: "events", calls: &calls},
		recordingRunDeleter{name: "artifacts", calls: &calls},
	)
	if err := coordinator.DeleteRun(context.Background(), " run-1 "); err != nil {
		t.Fatal(err)
	}
	want := []string{"checkpoints:run-1", "artifacts:run-1", "events:run-1", "execution:run-1"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("deletion calls = %#v, want %#v", calls, want)
	}
}

func TestRunDeletionCoordinatorStopsBeforeExecutionOnDependencyFailure(t *testing.T) {
	var calls []string
	failure := errors.New("artifact deletion failed")
	executionStore := &recordingExecutionStore{
		MemoryExecutionStore: NewMemoryExecutionStore(),
		name:                 "execution",
		calls:                &calls,
	}
	if err := executionStore.CreateRun(context.Background(), RunRecord{RunID: "run-1", RootRunID: "run-1", Status: RunStatusCompleted}); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	coordinator := NewRunDeletionCoordinator(
		executionStore,
		recordingRunDeleter{name: "checkpoints", calls: &calls},
		nil,
		recordingRunDeleter{name: "artifacts", calls: &calls, err: failure},
	)
	err := coordinator.DeleteRun(context.Background(), "run-1")
	if !errors.Is(err, failure) {
		t.Fatalf("DeleteRun() error = %v", err)
	}
	want := []string{"checkpoints:run-1", "artifacts:run-1"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("deletion calls = %#v, want %#v", calls, want)
	}
}

func TestRunDeletionCoordinatorRejectsTamperedChildLineage(t *testing.T) {
	tests := []struct {
		name  string
		child RunRecord
		want  string
	}{
		{
			name:  "parent",
			child: RunRecord{RunID: "child", ParentRunID: "unrelated", RootRunID: "root", Status: RunStatusCompleted},
			want:  "declares parent",
		},
		{
			name:  "root",
			child: RunRecord{RunID: "child", ParentRunID: "root", RootRunID: "unrelated", Status: RunStatusCompleted},
			want:  "declares root",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executionStore := NewMemoryExecutionStore()
			root := RunRecord{RunID: "root", RootRunID: "root", Status: RunStatusCompleted, ChildRunIDs: []string{"child"}}
			validChild := RunRecord{RunID: "child", ParentRunID: "root", RootRunID: "root", Status: RunStatusCompleted}
			for _, run := range []RunRecord{root, validChild} {
				if err := executionStore.CreateRun(context.Background(), run); err != nil {
					t.Fatalf("CreateRun(%q) error = %v", run.RunID, err)
				}
			}
			executionStore.mu.Lock()
			executionStore.runs[test.child.RunID] = cloneRunRecord(test.child)
			executionStore.mu.Unlock()
			var calls []string
			coordinator := NewRunDeletionCoordinator(
				executionStore,
				recordingRunDeleter{name: "checkpoints", calls: &calls},
				nil,
				nil,
			)
			err := coordinator.DeleteRun(context.Background(), root.RunID)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("DeleteRun() error = %v, want %q", err, test.want)
			}
			if len(calls) != 0 {
				t.Fatalf("deletion started before hierarchy validation: %#v", calls)
			}
			for _, runID := range []string{"root", "child"} {
				if _, err := executionStore.GetRun(context.Background(), runID); err != nil {
					t.Fatalf("GetRun(%q) after rejected deletion error = %v", runID, err)
				}
			}
		})
	}
}

func TestRunDeletionCoordinatorRejectsActiveDescendant(t *testing.T) {
	executionStore := NewMemoryExecutionStore()
	runs := []RunRecord{
		{RunID: "root", RootRunID: "root", Status: RunStatusCompleted, ChildRunIDs: []string{"child"}},
		{RunID: "child", ParentRunID: "root", RootRunID: "root", Status: RunStatusRunning},
	}
	for _, run := range runs {
		if err := executionStore.CreateRun(context.Background(), run); err != nil {
			t.Fatalf("CreateRun(%q) error = %v", run.RunID, err)
		}
	}
	var calls []string
	coordinator := NewRunDeletionCoordinator(
		executionStore,
		recordingRunDeleter{name: "checkpoints", calls: &calls},
		nil,
		nil,
	)
	err := coordinator.DeleteRun(context.Background(), "root")
	if !errors.Is(err, ErrRunControlNotAllowed) || !strings.Contains(err.Error(), "child") {
		t.Fatalf("DeleteRun() error = %v, want active child rejection", err)
	}
	if len(calls) != 0 {
		t.Fatalf("deletion started before active descendant validation: %#v", calls)
	}
	for _, runID := range []string{"root", "child"} {
		if _, err := executionStore.GetRun(context.Background(), runID); err != nil {
			t.Fatalf("GetRun(%q) after rejected deletion error = %v", runID, err)
		}
	}
}

func TestRunDeletionCoordinatorRejectsUnknownStatus(t *testing.T) {
	executionStore := NewMemoryExecutionStore()
	if err := executionStore.CreateRun(context.Background(), RunRecord{RunID: "root", RootRunID: "root", Status: RunStatus("corrupt")}); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	coordinator := NewRunDeletionCoordinator(executionStore, nil, nil, nil)
	if err := coordinator.DeleteRun(context.Background(), "root"); err == nil || !strings.Contains(err.Error(), "unsupported deletion status") {
		t.Fatalf("DeleteRun() error = %v, want corrupt status rejection", err)
	}
	if _, err := executionStore.GetRun(context.Background(), "root"); err != nil {
		t.Fatalf("GetRun() after rejected deletion error = %v", err)
	}
}

func TestRunDeletionCoordinatorResumesUnlinkedDeletionWithStableID(t *testing.T) {
	ctx := context.Background()
	executionStore := NewMemoryExecutionStore()
	if err := executionStore.CreateRun(ctx, RunRecord{RunID: "root", RootRunID: "root", Status: RunStatusCompleted}); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	checkpointStore := &flakyCheckpointDeletionStore{
		MemoryCheckpointStore: NewMemoryCheckpointStore(),
		failures:              1,
	}
	coordinator := NewRunDeletionCoordinator(executionStore, checkpointStore, nil, nil)
	if err := coordinator.DeleteRun(ctx, "root"); err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("first DeleteRun() error = %v, want interruption", err)
	}
	reserved, err := executionStore.GetRun(ctx, "root")
	if err != nil {
		t.Fatalf("GetRun() after interruption error = %v", err)
	}
	if reserved.Deletion == nil || reserved.Deletion.Phase != RunDeletionUnlinked {
		t.Fatalf("deletion after interruption = %#v, want unlinked", reserved.Deletion)
	}
	deletionID := reserved.Deletion.ID
	if err := coordinator.DeleteRun(ctx, "root"); err != nil {
		t.Fatalf("retry DeleteRun() error = %v", err)
	}
	if _, err := executionStore.GetRun(ctx, "root"); !errors.Is(err, ErrRunnerRecordNotFound) {
		t.Fatalf("GetRun() after retry error = %v, want not found", err)
	}
	checkpointStore.mu.Lock()
	fenceIDs := append([]string(nil), checkpointStore.fenceIDs...)
	checkpointStore.mu.Unlock()
	if len(fenceIDs) != 2 || fenceIDs[0] != deletionID || fenceIDs[1] != deletionID {
		t.Fatalf("fence IDs = %#v, want stable %q", fenceIDs, deletionID)
	}
}

func TestRunDeletionCoordinatorReconcilesDurableManifestAfterInterruption(t *testing.T) {
	ctx := context.Background()
	runtimeStore := NewMemoryRuntimeStore()
	if err := runtimeStore.CreateRun(ctx, RunRecord{RunID: "root", RootRunID: "root", Status: RunStatusCompleted}); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	checkpointStore := &flakyCheckpointDeletionStore{
		MemoryCheckpointStore: NewMemoryCheckpointStore(),
		failures:              1,
	}
	coordinator := NewRunDeletionCoordinator(runtimeStore, checkpointStore, nil, nil)
	if err := coordinator.DeleteRun(ctx, "root"); err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("first DeleteRun() error = %v, want interruption", err)
	}
	manifests, err := runtimeStore.ListRunDeletionManifests(ctx)
	if err != nil || len(manifests) != 1 {
		t.Fatalf("ListRunDeletionManifests() = %#v, error = %v", manifests, err)
	}
	if manifests[0].Phase != RunDeletionUnlinked {
		t.Fatalf("manifest phase = %q, want unlinked", manifests[0].Phase)
	}
	if err := coordinator.ReconcileRunDeletions(ctx); err != nil {
		t.Fatalf("ReconcileRunDeletions() error = %v", err)
	}
	manifest, err := runtimeStore.LoadRunDeletionManifest(ctx, manifests[0].ID)
	if err != nil {
		t.Fatalf("LoadRunDeletionManifest() error = %v", err)
	}
	if manifest.Phase != RunDeletionDeleted {
		t.Fatalf("manifest phase after reconciliation = %q, want deleted", manifest.Phase)
	}
	if _, err := runtimeStore.GetRun(ctx, "root"); !errors.Is(err, ErrRunnerRecordNotFound) {
		t.Fatalf("GetRun() after reconciliation error = %v, want not found", err)
	}
	if err := coordinator.ReconcileRunDeletions(ctx); err != nil {
		t.Fatalf("second ReconcileRunDeletions() error = %v", err)
	}
}

func TestRunDeletionReconciliationFailsClosedForMissingPlannedRun(t *testing.T) {
	ctx := context.Background()
	runtimeStore := NewMemoryRuntimeStore()
	run := RunRecord{RunID: "root", RootRunID: "root", Status: RunStatusCompleted}
	if err := runtimeStore.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	run.Deletion = &RunDeletionState{ID: "deletion", RootRunID: run.RunID, Phase: RunDeletionReserved}
	reserved, err := runtimeStore.CompareAndSwapRun(withRunDeletionMutation(ctx, run.Deletion.ID), run.Revision, run)
	if err != nil {
		t.Fatalf("reserve deletion error = %v", err)
	}
	reserved.Deletion.Phase = RunDeletionPlanned
	reserved.Deletion.RunIDs = []string{"missing-child", run.RunID}
	planned, err := runtimeStore.CompareAndSwapRun(withRunDeletionMutation(ctx, run.Deletion.ID), reserved.Revision, reserved)
	if err != nil {
		t.Fatalf("persist deletion plan error = %v", err)
	}
	now := time.Now().UTC()
	if err := runtimeStore.SaveRunDeletionManifest(ctx, RunDeletionManifest{
		ID: planned.Deletion.ID, RootRunID: planned.RunID, Phase: RunDeletionPlanned,
		RunIDs: append([]string(nil), planned.Deletion.RunIDs...), CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveRunDeletionManifest() error = %v", err)
	}
	coordinator := NewRunDeletionCoordinator(runtimeStore, nil, nil, nil)
	if err := coordinator.ReconcileRunDeletions(ctx); err == nil || !strings.Contains(err.Error(), "planned run") {
		t.Fatalf("ReconcileRunDeletions() error = %v, want missing planned run rejection", err)
	}
	if _, err := runtimeStore.GetRun(ctx, run.RunID); err != nil {
		t.Fatalf("GetRun() after rejected reconciliation error = %v", err)
	}
	manifest, err := runtimeStore.LoadRunDeletionManifest(ctx, run.Deletion.ID)
	if err != nil || manifest.Phase != RunDeletionPlanned {
		t.Fatalf("manifest after rejected reconciliation = %#v, error = %v", manifest, err)
	}
}

func TestRunDeletionCoordinatorAllowsChildUnlinkDuringUnplannedParentDeletion(t *testing.T) {
	ctx := context.Background()
	executionStore := NewMemoryExecutionStore()
	parent := RunRecord{
		RunID: "parent", RootRunID: "parent", Status: RunStatusCompleted,
		ChildRunIDs: []string{"child"},
	}
	child := RunRecord{
		RunID: "child", ParentRunID: "parent", RootRunID: "parent", Status: RunStatusCompleted,
	}
	for _, run := range []RunRecord{parent, child} {
		if err := executionStore.CreateRun(ctx, run); err != nil {
			t.Fatalf("CreateRun(%q) error = %v", run.RunID, err)
		}
	}
	checkpointStore := &hookedCheckpointDeletionStore{MemoryCheckpointStore: NewMemoryCheckpointStore()}
	checkpointStore.hook = func(hookCtx context.Context) error {
		for {
			current, err := executionStore.GetRun(hookCtx, "parent")
			if err != nil {
				return err
			}
			current.Deletion = &RunDeletionState{
				ID: "parent-deletion", RootRunID: "parent", Phase: RunDeletionReserved,
			}
			_, err = executionStore.CompareAndSwapRun(withRunDeletionMutation(hookCtx, "parent-deletion"), current.Revision, current)
			if errors.Is(err, ErrRunRevisionConflict) {
				continue
			}
			return err
		}
	}
	childCoordinator := NewRunDeletionCoordinator(executionStore, checkpointStore, nil, nil)
	if err := childCoordinator.DeleteRun(ctx, "child"); err != nil {
		t.Fatalf("delete child during parent reservation error = %v", err)
	}
	reservedParent, err := executionStore.GetRun(ctx, "parent")
	if err != nil {
		t.Fatalf("GetRun(parent) error = %v", err)
	}
	if len(reservedParent.ChildRunIDs) != 0 {
		t.Fatalf("parent child links = %#v, want empty", reservedParent.ChildRunIDs)
	}
	if reservedParent.Deletion == nil || reservedParent.Deletion.ID != "parent-deletion" || reservedParent.Deletion.Phase != RunDeletionReserved {
		t.Fatalf("parent deletion = %#v", reservedParent.Deletion)
	}
	parentCoordinator := NewRunDeletionCoordinator(executionStore, checkpointStore, nil, nil)
	if err := parentCoordinator.DeleteRun(ctx, "parent"); err != nil {
		t.Fatalf("resume parent deletion error = %v", err)
	}
	if _, err := executionStore.GetRun(ctx, "parent"); !errors.Is(err, ErrRunnerRecordNotFound) {
		t.Fatalf("GetRun(parent) after deletion error = %v, want not found", err)
	}
}

func TestRunDeletionCoordinatorFreezesDescendantHierarchyBeforePlanning(t *testing.T) {
	ctx := context.Background()
	baseStore := NewMemoryExecutionStore()
	runs := []RunRecord{
		{RunID: "root", RootRunID: "root", Status: RunStatusCompleted, ChildRunIDs: []string{"child"}},
		{RunID: "child", ParentRunID: "root", RootRunID: "root", Status: RunStatusPaused},
		{RunID: "grandchild", ParentRunID: "child", RootRunID: "root", Status: RunStatusCompleted},
	}
	for _, run := range runs {
		if err := baseStore.CreateRun(ctx, run); err != nil {
			t.Fatalf("CreateRun(%q) error = %v", run.RunID, err)
		}
	}

	executionStore := &mutatingHierarchyExecutionStore{
		MemoryExecutionStore: baseStore,
		targetRunID:          "child",
	}
	executionStore.hook = func(hookCtx context.Context) error {
		child, err := baseStore.GetRun(hookCtx, "child")
		if err != nil {
			return err
		}
		child.ChildRunIDs = append(child.ChildRunIDs, "grandchild")
		_, err = baseStore.CompareAndSwapRun(hookCtx, child.Revision, child)
		return err
	}

	coordinator := NewRunDeletionCoordinator(executionStore, nil, nil, nil)
	if err := coordinator.DeleteRun(ctx, "root"); err != nil {
		t.Fatalf("DeleteRun() error = %v", err)
	}
	for _, runID := range []string{"root", "child", "grandchild"} {
		if _, err := baseStore.GetRun(ctx, runID); !errors.Is(err, ErrRunnerRecordNotFound) {
			t.Fatalf("GetRun(%q) after deletion error = %v, want not found", runID, err)
		}
	}
}
