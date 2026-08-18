package runtime

import (
	"context"
	"errors"
	"fmt"
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

type deletionCrashController struct {
	point   string
	crashed bool
}

func (controller *deletionCrashController) after(point string) error {
	if controller == nil || controller.crashed || controller.point != point {
		return nil
	}
	controller.crashed = true
	return fmt.Errorf("injected crash after %s", point)
}

type crashingDeletionRuntimeStore struct {
	*MemoryRuntimeStore
	controller *deletionCrashController
}

func (store *crashingDeletionRuntimeStore) CompareAndSwapRun(ctx context.Context, expectedRevision uint64, run RunRecord) (RunRecord, error) {
	updated, err := store.MemoryRuntimeStore.CompareAndSwapRun(ctx, expectedRevision, run)
	if err != nil {
		return updated, err
	}
	if _, ok := ctx.Value(runDeletionUnlinkMutationKey{}).(runDeletionUnlinkMutation); ok {
		if crashErr := store.controller.after("parent unlink"); crashErr != nil {
			return updated, crashErr
		}
	}
	if run.Deletion == nil {
		return updated, nil
	}
	point := ""
	switch {
	case run.RunID == "deletion-root" && run.Deletion.Phase == RunDeletionReserved:
		point = "root reservation"
	case run.RunID == "descendant" && run.Deletion.Phase == RunDeletionReserved:
		point = "descendant reservation"
	case run.RunID == "deletion-root" && run.Deletion.Phase == RunDeletionPlanned:
		point = "plan"
	}
	if crashErr := store.controller.after(point); crashErr != nil {
		return updated, crashErr
	}
	return updated, nil
}

func (store *crashingDeletionRuntimeStore) SaveRunDeletionManifest(ctx context.Context, manifest RunDeletionManifest) error {
	if err := store.MemoryRuntimeStore.SaveRunDeletionManifest(ctx, manifest); err != nil {
		return err
	}
	point := ""
	switch manifest.Phase {
	case RunDeletionPlanned:
		point = "planned manifest"
	case RunDeletionUnlinked:
		point = "unlinked manifest"
	case RunDeletionDeleted:
		point = "completion manifest"
	}
	return store.controller.after(point)
}

func (store *crashingDeletionRuntimeStore) FenceRunDeletion(ctx context.Context, runID, deletionID string) error {
	if err := store.MemoryRuntimeStore.FenceRunDeletion(ctx, runID, deletionID); err != nil {
		return err
	}
	return store.controller.after("execution fence")
}

func (store *crashingDeletionRuntimeStore) DeleteRun(ctx context.Context, runID string) error {
	if err := store.MemoryRuntimeStore.DeleteRun(ctx, runID); err != nil {
		return err
	}
	if runID != "deletion-root" {
		return nil
	}
	return store.controller.after("execution delete")
}

type crashingRunDeletionStore struct {
	RunDeleter
	fencer     RunDeletionFencer
	name       string
	controller *deletionCrashController
}

func (store *crashingRunDeletionStore) FenceRunDeletion(ctx context.Context, runID, deletionID string) error {
	if err := store.fencer.FenceRunDeletion(ctx, runID, deletionID); err != nil {
		return err
	}
	return store.controller.after(store.name + " fence")
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

func TestRunDeletionReconciliationResumesReservedHierarchyBeforeManifest(t *testing.T) {
	ctx := context.Background()
	runtimeStore := NewMemoryRuntimeStore()
	root := RunRecord{
		RunID: "z-root", RootRunID: "z-root", Status: RunStatusCompleted,
		ChildRunIDs: []string{"a-child"},
	}
	child := RunRecord{
		RunID: "a-child", ParentRunID: root.RunID, RootRunID: root.RunID, Status: RunStatusCompleted,
	}
	for _, run := range []RunRecord{root, child} {
		if err := runtimeStore.CreateRun(ctx, run); err != nil {
			t.Fatalf("CreateRun(%q) error = %v", run.RunID, err)
		}
	}
	deletionID := "interrupted-deletion"
	root.Deletion = &RunDeletionState{ID: deletionID, RootRunID: root.RunID, Phase: RunDeletionReserved}
	if _, err := runtimeStore.CompareAndSwapRun(withRunDeletionMutation(ctx, deletionID), root.Revision, root); err != nil {
		t.Fatalf("reserve root deletion error = %v", err)
	}
	child.Deletion = &RunDeletionState{ID: deletionID, RootRunID: root.RunID, Phase: RunDeletionReserved}
	if _, err := runtimeStore.CompareAndSwapRun(withRunDeletionMutation(ctx, deletionID), child.Revision, child); err != nil {
		t.Fatalf("reserve child deletion error = %v", err)
	}

	coordinator := NewRunDeletionCoordinator(runtimeStore, nil, nil, nil)
	if err := coordinator.ReconcileRunDeletions(ctx); err != nil {
		t.Fatalf("ReconcileRunDeletions() error = %v", err)
	}
	for _, runID := range []string{root.RunID, child.RunID} {
		if _, err := runtimeStore.GetRun(ctx, runID); !errors.Is(err, ErrRunnerRecordNotFound) {
			t.Fatalf("GetRun(%q) after reconciliation error = %v, want not found", runID, err)
		}
	}
	manifest, err := runtimeStore.LoadRunDeletionManifest(ctx, deletionID)
	if err != nil {
		t.Fatalf("LoadRunDeletionManifest() error = %v", err)
	}
	if manifest.Phase != RunDeletionDeleted {
		t.Fatalf("manifest phase = %q, want deleted", manifest.Phase)
	}
}

func TestRunDeletionReconciliationResumesEveryDurableStage(t *testing.T) {
	crashPoints := []string{
		"root reservation",
		"descendant reservation",
		"plan",
		"planned manifest",
		"checkpoint fence",
		"artifact fence",
		"event fence",
		"execution fence",
		"parent unlink",
		"unlinked manifest",
		"execution delete",
		"completion manifest",
	}
	for _, crashPoint := range crashPoints {
		t.Run(crashPoint, func(t *testing.T) {
			ctx := context.Background()
			baseStore := NewMemoryRuntimeStore()
			parent := RunRecord{
				RunID: "parent", RootRunID: "parent", Status: RunStatusCompleted,
				ChildRunIDs: []string{"deletion-root"},
			}
			root := RunRecord{
				RunID: "deletion-root", ParentRunID: parent.RunID, RootRunID: parent.RunID,
				Status: RunStatusCompleted, ChildRunIDs: []string{"descendant"},
			}
			descendant := RunRecord{
				RunID: "descendant", ParentRunID: root.RunID, RootRunID: parent.RunID, Status: RunStatusCompleted,
			}
			for _, run := range []RunRecord{parent, root, descendant} {
				if err := baseStore.CreateRun(ctx, run); err != nil {
					t.Fatalf("CreateRun(%q) error = %v", run.RunID, err)
				}
			}
			controller := &deletionCrashController{point: crashPoint}
			executionStore := &crashingDeletionRuntimeStore{MemoryRuntimeStore: baseStore, controller: controller}
			checkpointBase := NewMemoryCheckpointStore()
			checkpointStore := &crashingRunDeletionStore{
				RunDeleter: checkpointBase, fencer: checkpointBase, name: "checkpoint", controller: controller,
			}
			artifactBase := NewMemoryArtifactStore()
			artifactStore := &crashingRunDeletionStore{
				RunDeleter: artifactBase, fencer: artifactBase, name: "artifact", controller: controller,
			}
			eventBase := NewMemoryEventSink()
			eventStore := &crashingRunDeletionStore{
				RunDeleter: eventBase, fencer: eventBase, name: "event", controller: controller,
			}
			coordinator := NewRunDeletionCoordinator(executionStore, checkpointStore, eventStore, artifactStore)
			if err := coordinator.DeleteRun(ctx, root.RunID); err == nil || !strings.Contains(err.Error(), "injected crash") {
				t.Fatalf("DeleteRun() error = %v, want injected crash", err)
			}
			if !controller.crashed {
				t.Fatalf("crash point %q was not reached", crashPoint)
			}

			reconciler := NewRunDeletionCoordinator(baseStore, checkpointBase, eventBase, artifactBase)
			if err := reconciler.ReconcileRunDeletions(ctx); err != nil {
				t.Fatalf("ReconcileRunDeletions() after %q error = %v", crashPoint, err)
			}
			for _, runID := range []string{root.RunID, descendant.RunID} {
				if _, err := baseStore.GetRun(ctx, runID); !errors.Is(err, ErrRunnerRecordNotFound) {
					t.Fatalf("GetRun(%q) after reconciliation error = %v, want not found", runID, err)
				}
			}
			persistedParent, err := baseStore.GetRun(ctx, parent.RunID)
			if err != nil {
				t.Fatalf("GetRun(parent) error = %v", err)
			}
			if len(persistedParent.ChildRunIDs) != 0 {
				t.Fatalf("parent child links = %#v, want empty", persistedParent.ChildRunIDs)
			}
			manifests, err := baseStore.ListRunDeletionManifests(ctx)
			if err != nil || len(manifests) != 1 || manifests[0].Phase != RunDeletionDeleted {
				t.Fatalf("deletion manifests = %#v, error = %v", manifests, err)
			}
			if err := reconciler.ReconcileRunDeletions(ctx); err != nil {
				t.Fatalf("second ReconcileRunDeletions() error = %v", err)
			}
		})
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
