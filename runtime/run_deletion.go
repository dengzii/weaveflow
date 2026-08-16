package runtime

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

type RunDeleter interface {
	DeleteRun(context.Context, string) error
}

type RunDeletionExecutionStore interface {
	RunDeleter
	GetRun(context.Context, string) (RunRecord, error)
	CompareAndSwapRun(context.Context, uint64, RunRecord) (RunRecord, error)
}

type RunDeletionFencer interface {
	FenceRunDeletion(context.Context, string, string) error
}

type RunDeletionCoordinator struct {
	executionStore  RunDeletionExecutionStore
	checkpointStore RunDeleter
	eventStore      RunDeleter
	artifactStore   RunDeleter
}

type runDeletionStoreTarget struct {
	name  string
	store RunDeleter
}

type runHierarchyReader interface {
	GetRun(context.Context, string) (RunRecord, error)
}

type runDeletionMutationKey struct{}

type runDeletionUnlinkMutation struct {
	deletionID string
	childRunID string
}

type runDeletionUnlinkMutationKey struct{}

func NewRunDeletionCoordinator(
	executionStore RunDeletionExecutionStore,
	checkpointStore RunDeleter,
	eventStore RunDeleter,
	artifactStore RunDeleter,
) *RunDeletionCoordinator {
	return &RunDeletionCoordinator{
		executionStore:  executionStore,
		checkpointStore: checkpointStore,
		eventStore:      eventStore,
		artifactStore:   artifactStore,
	}
}

func (coordinator *RunDeletionCoordinator) DeleteRun(ctx context.Context, runID string) error {
	if coordinator == nil || coordinator.executionStore == nil {
		return fmt.Errorf("run deletion execution store is required")
	}
	ctx = normalizeRunnerContext(ctx)
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return ErrRunnerRecordNotFound
	}
	stores := uniqueRunDeletionStores(coordinator.executionStore, []runDeletionStoreTarget{
		{name: "checkpoints", store: coordinator.checkpointStore},
		{name: "artifacts", store: coordinator.artifactStore},
		{name: "events", store: coordinator.eventStore},
	})
	for _, target := range stores {
		if target.store == nil {
			continue
		}
		if _, ok := target.store.(RunDeletionFencer); !ok {
			return fmt.Errorf("%s store does not support run deletion fencing", target.name)
		}
	}
	deletion, err := prepareRunDeletion(ctx, coordinator.executionStore, runID)
	if err != nil {
		return err
	}
	deletionCtx := withRunDeletionMutation(ctx, deletion.ID)
	for _, targetRunID := range deletion.RunIDs {
		for _, target := range stores {
			if target.store == nil {
				continue
			}
			fencer := target.store.(RunDeletionFencer)
			if err := fencer.FenceRunDeletion(deletionCtx, targetRunID, deletion.ID); err != nil {
				return fmt.Errorf("fence run %q in %s store: %w", targetRunID, target.name, err)
			}
		}
	}
	if deletion.Phase != RunDeletionUnlinked {
		if err := unlinkRunDeletionRoot(deletionCtx, coordinator.executionStore, deletion); err != nil {
			return err
		}
		deletion, err = advanceRunDeletionPhase(deletionCtx, coordinator.executionStore, deletion.RootRunID, deletion.ID, RunDeletionUnlinked)
		if err != nil {
			return err
		}
	}
	for _, targetRunID := range deletion.RunIDs {
		for _, target := range stores {
			if target.store == nil {
				continue
			}
			if err := target.store.DeleteRun(deletionCtx, targetRunID); err != nil && !errors.Is(err, ErrRunnerRecordNotFound) {
				return fmt.Errorf("delete run %q from %s store: %w", targetRunID, target.name, err)
			}
		}
	}
	return nil
}

func uniqueRunDeletionStores(executionStore RunDeletionExecutionStore, auxiliary []runDeletionStoreTarget) []runDeletionStoreTarget {
	stores := make([]runDeletionStoreTarget, 0, len(auxiliary)+1)
	for _, target := range auxiliary {
		if target.store == nil || sameRunDeletionStore(target.store, executionStore) {
			continue
		}
		duplicate := false
		for _, existing := range stores {
			if sameRunDeletionStore(target.store, existing.store) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			stores = append(stores, target)
		}
	}
	return append(stores, runDeletionStoreTarget{name: "execution", store: executionStore})
}

func prepareRunDeletion(ctx context.Context, store RunDeletionExecutionStore, rootID string) (RunDeletionState, error) {
	var deletionID string
	revisionConflicts := 0
	for {
		root, err := store.GetRun(ctx, rootID)
		if err != nil {
			return RunDeletionState{}, err
		}
		if root.RunID != rootID {
			return RunDeletionState{}, fmt.Errorf("run %q deletion lookup returned run %q", rootID, root.RunID)
		}
		if root.Deletion == nil || root.Deletion.Phase != RunDeletionUnlinked {
			if err := validateRequestedRunDeletionParent(ctx, store, root); err != nil {
				return RunDeletionState{}, err
			}
		}
		if root.Deletion != nil {
			if err := validateRunDeletionState(root.Deletion); err != nil {
				return RunDeletionState{}, fmt.Errorf("run %q deletion state: %w", rootID, err)
			}
			if root.Deletion.RootRunID != rootID {
				return RunDeletionState{}, fmt.Errorf("run %q is already included in deletion of run %q", rootID, root.Deletion.RootRunID)
			}
			if err := validateRunDeletionStatus(root); err != nil {
				return RunDeletionState{}, err
			}
			deletionID = root.Deletion.ID
			if len(root.Deletion.RunIDs) > 0 {
				plan := append([]string(nil), root.Deletion.RunIDs...)
				if err := validateRunDeletionPlan(rootID, plan); err != nil {
					return RunDeletionState{}, err
				}
				if err := reserveRunDeletionPlan(ctx, store, deletionID, rootID, plan); err != nil {
					return RunDeletionState{}, err
				}
				deletion := *root.Deletion
				deletion.RunIDs = plan
				return deletion, nil
			}
			break
		}
		if err := validateRunDeletionStatus(root); err != nil {
			return RunDeletionState{}, err
		}
		deletionID = newRunnerID()
		root.Deletion = &RunDeletionState{
			ID: deletionID, RootRunID: rootID, ParentRunID: root.ParentRunID, Phase: RunDeletionReserved,
		}
		updated, err := store.CompareAndSwapRun(withRunDeletionMutation(ctx, deletionID), root.Revision, root)
		if errors.Is(err, ErrRunRevisionConflict) {
			revisionConflicts++
			if revisionConflicts >= runRevisionRetryLimit {
				return RunDeletionState{}, runRevisionRetriesExceeded("prepare run deletion")
			}
			continue
		}
		if err != nil {
			return RunDeletionState{}, fmt.Errorf("reserve run %q for deletion: %w", rootID, err)
		}
		if updated.Deletion == nil || updated.Deletion.ID != deletionID {
			return RunDeletionState{}, fmt.Errorf("run %q deletion reservation was not persisted", rootID)
		}
		break
	}

	runIDs, err := collectDescendantRunIDs(ctx, store, rootID, deletionID)
	if err != nil {
		return RunDeletionState{}, err
	}
	if err := reserveRunDeletionPlan(ctx, store, deletionID, rootID, runIDs); err != nil {
		return RunDeletionState{}, err
	}
	deletion, err := persistRunDeletionPlan(ctx, store, rootID, deletionID, runIDs)
	if err != nil {
		return RunDeletionState{}, err
	}
	return deletion, nil
}

func reserveRunDeletionPlan(ctx context.Context, store RunDeletionExecutionStore, deletionID, rootID string, runIDs []string) error {
	for _, runID := range runIDs {
		if runID == rootID {
			continue
		}
		if err := reserveRunDeletion(ctx, store, runID, deletionID, rootID); err != nil {
			return err
		}
	}
	return nil
}

func reserveRunDeletion(ctx context.Context, store RunDeletionExecutionStore, runID, deletionID, rootID string) error {
	revisionConflicts := 0
	for {
		run, err := store.GetRun(ctx, runID)
		if errors.Is(err, ErrRunnerRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := validateRunDeletionStatus(run); err != nil {
			return err
		}
		if run.Deletion != nil {
			if err := validateRunDeletionState(run.Deletion); err != nil {
				return fmt.Errorf("run %q deletion state: %w", runID, err)
			}
			if run.Deletion.ID != deletionID {
				return fmt.Errorf("run %q is reserved by deletion %q, not %q", runID, run.Deletion.ID, deletionID)
			}
			if run.Deletion.RootRunID != rootID {
				return fmt.Errorf("run %q is reserved for deletion root %q, not %q", runID, run.Deletion.RootRunID, rootID)
			}
			return nil
		}
		run.Deletion = &RunDeletionState{ID: deletionID, RootRunID: rootID, Phase: RunDeletionReserved}
		_, err = store.CompareAndSwapRun(withRunDeletionMutation(ctx, deletionID), run.Revision, run)
		if errors.Is(err, ErrRunRevisionConflict) {
			revisionConflicts++
			if revisionConflicts >= runRevisionRetryLimit {
				return runRevisionRetriesExceeded("reserve run deletion")
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("reserve run %q for deletion: %w", runID, err)
		}
		return nil
	}
}

func persistRunDeletionPlan(ctx context.Context, store RunDeletionExecutionStore, rootID, deletionID string, runIDs []string) (RunDeletionState, error) {
	if err := validateRunDeletionPlan(rootID, runIDs); err != nil {
		return RunDeletionState{}, err
	}
	revisionConflicts := 0
	for {
		root, err := store.GetRun(ctx, rootID)
		if err != nil {
			return RunDeletionState{}, err
		}
		if root.Deletion == nil || root.Deletion.ID != deletionID {
			return RunDeletionState{}, fmt.Errorf("run %q deletion reservation changed before plan persistence", rootID)
		}
		if len(root.Deletion.RunIDs) > 0 {
			if !slices.Equal(root.Deletion.RunIDs, runIDs) {
				return RunDeletionState{}, fmt.Errorf("run %q deletion plan changed", rootID)
			}
			deletion := *root.Deletion
			deletion.RunIDs = append([]string(nil), root.Deletion.RunIDs...)
			return deletion, nil
		}
		deletion := *root.Deletion
		deletion.Phase = RunDeletionPlanned
		deletion.RunIDs = append([]string(nil), runIDs...)
		root.Deletion = &deletion
		_, err = store.CompareAndSwapRun(withRunDeletionMutation(ctx, deletionID), root.Revision, root)
		if errors.Is(err, ErrRunRevisionConflict) {
			revisionConflicts++
			if revisionConflicts >= runRevisionRetryLimit {
				return RunDeletionState{}, runRevisionRetriesExceeded("persist run deletion plan")
			}
			continue
		}
		if err != nil {
			return RunDeletionState{}, fmt.Errorf("persist run %q deletion plan: %w", rootID, err)
		}
		return deletion, nil
	}
}

func unlinkRunDeletionRoot(ctx context.Context, store RunDeletionExecutionStore, deletion RunDeletionState) error {
	parentRunID := strings.TrimSpace(deletion.ParentRunID)
	if parentRunID == "" {
		return nil
	}
	if parentRunID == deletion.RootRunID {
		return fmt.Errorf("run %q cannot be its own deletion parent", deletion.RootRunID)
	}
	revisionConflicts := 0
	for {
		root, err := store.GetRun(ctx, deletion.RootRunID)
		if err != nil {
			return fmt.Errorf("load run %q before parent unlink: %w", deletion.RootRunID, err)
		}
		if root.Deletion == nil || root.Deletion.ID != deletion.ID {
			return fmt.Errorf("run %q deletion reservation changed before parent unlink", deletion.RootRunID)
		}
		if root.ParentRunID != parentRunID {
			return fmt.Errorf("run %q declares parent %q, expected %q", deletion.RootRunID, root.ParentRunID, parentRunID)
		}
		parent, err := store.GetRun(ctx, parentRunID)
		if err != nil {
			return fmt.Errorf("load parent run %q for deletion of %q: %w", parentRunID, deletion.RootRunID, err)
		}
		if parent.RunID != parentRunID {
			return fmt.Errorf("parent run %q deletion lookup returned run %q", parentRunID, parent.RunID)
		}
		if !isTerminalRunStatus(parent.Status) {
			return fmt.Errorf("%w: parent run %q status %q must be terminal before deleting child run %q", ErrRunControlNotAllowed, parentRunID, parent.Status, deletion.RootRunID)
		}
		if parent.RootRunID != root.RootRunID {
			return fmt.Errorf("run %q declares root %q, but parent run %q declares root %q", deletion.RootRunID, root.RootRunID, parentRunID, parent.RootRunID)
		}
		if parent.Deletion != nil {
			if err := validateRunDeletionState(parent.Deletion); err != nil {
				return fmt.Errorf("parent run %q deletion state: %w", parentRunID, err)
			}
			if parent.Deletion.Phase != RunDeletionReserved || len(parent.Deletion.RunIDs) != 0 {
				return fmt.Errorf("run %q cannot be unlinked after parent run %q deletion %q was planned", deletion.RootRunID, parentRunID, parent.Deletion.ID)
			}
		}
		childLinked := slices.Contains(parent.ChildRunIDs, deletion.RootRunID)
		childPending := false
		for _, pending := range parent.PendingChildRuns {
			if pending.ChildRunID == deletion.RootRunID {
				childPending = true
				break
			}
		}
		if !childLinked && !childPending {
			return nil
		}
		children := make([]string, 0, len(parent.ChildRunIDs))
		for _, childRunID := range parent.ChildRunIDs {
			if childRunID != deletion.RootRunID {
				children = append(children, childRunID)
			}
		}
		parent.ChildRunIDs = children
		pendingChildren := make([]PendingChildRun, 0, len(parent.PendingChildRuns))
		for _, pending := range parent.PendingChildRuns {
			if pending.ChildRunID != deletion.RootRunID {
				pendingChildren = append(pendingChildren, pending)
			}
		}
		parent.PendingChildRuns = pendingChildren
		unlinkCtx := withRunDeletionUnlinkMutation(ctx, deletion.ID, deletion.RootRunID)
		_, err = store.CompareAndSwapRun(unlinkCtx, parent.Revision, parent)
		if errors.Is(err, ErrRunRevisionConflict) {
			revisionConflicts++
			if revisionConflicts >= runRevisionRetryLimit {
				return runRevisionRetriesExceeded("unlink run deletion root")
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("unlink run %q from parent %q: %w", deletion.RootRunID, parentRunID, err)
		}
		return nil
	}
}

func validateRequestedRunDeletionParent(ctx context.Context, store RunDeletionExecutionStore, root RunRecord) error {
	if root.ParentRunID == "" {
		if root.RootRunID != root.RunID {
			return fmt.Errorf("root run %q declares root run %q", root.RunID, root.RootRunID)
		}
		return nil
	}
	parent, err := store.GetRun(ctx, root.ParentRunID)
	if err != nil {
		return fmt.Errorf("load parent run %q for deletion of %q: %w", root.ParentRunID, root.RunID, err)
	}
	if parent.RunID != root.ParentRunID {
		return fmt.Errorf("parent run %q deletion lookup returned run %q", root.ParentRunID, parent.RunID)
	}
	if !isTerminalRunStatus(parent.Status) {
		return fmt.Errorf("%w: parent run %q status %q must be terminal before deleting child run %q", ErrRunControlNotAllowed, parent.RunID, parent.Status, root.RunID)
	}
	if parent.RootRunID != root.RootRunID {
		return fmt.Errorf("run %q declares root %q, but parent run %q declares root %q", root.RunID, root.RootRunID, parent.RunID, parent.RootRunID)
	}
	childCount := 0
	for _, childRunID := range parent.ChildRunIDs {
		if childRunID == root.RunID {
			childCount++
		}
	}
	for _, pending := range parent.PendingChildRuns {
		if pending.ChildRunID == root.RunID {
			childCount++
		}
	}
	if childCount > 1 || childCount == 0 && root.Deletion == nil {
		return fmt.Errorf("parent run %q has invalid child link count %d for run %q", parent.RunID, childCount, root.RunID)
	}
	if parent.Deletion == nil {
		return nil
	}
	if err := validateRunDeletionState(parent.Deletion); err != nil {
		return fmt.Errorf("parent run %q deletion state: %w", parent.RunID, err)
	}
	if root.Deletion == nil {
		return fmt.Errorf("run %q cannot begin deletion while parent run %q is reserved by deletion %q", root.RunID, parent.RunID, parent.Deletion.ID)
	}
	if parent.Deletion.Phase != RunDeletionReserved || len(parent.Deletion.RunIDs) != 0 {
		return fmt.Errorf("run %q cannot continue deletion after parent run %q deletion %q was planned", root.RunID, parent.RunID, parent.Deletion.ID)
	}
	return nil
}

func advanceRunDeletionPhase(
	ctx context.Context,
	store RunDeletionExecutionStore,
	rootRunID string,
	deletionID string,
	phase RunDeletionPhase,
) (RunDeletionState, error) {
	revisionConflicts := 0
	for {
		root, err := store.GetRun(ctx, rootRunID)
		if err != nil {
			return RunDeletionState{}, err
		}
		if root.Deletion == nil || root.Deletion.ID != deletionID {
			return RunDeletionState{}, fmt.Errorf("run %q deletion reservation changed before phase %q", rootRunID, phase)
		}
		if err := validateRunDeletionState(root.Deletion); err != nil {
			return RunDeletionState{}, fmt.Errorf("run %q deletion state: %w", rootRunID, err)
		}
		if root.Deletion.Phase == phase {
			deletion := *root.Deletion
			deletion.RunIDs = append([]string(nil), root.Deletion.RunIDs...)
			return deletion, nil
		}
		deletion := *root.Deletion
		deletion.Phase = phase
		root.Deletion = &deletion
		updated, err := store.CompareAndSwapRun(withRunDeletionMutation(ctx, deletionID), root.Revision, root)
		if errors.Is(err, ErrRunRevisionConflict) {
			revisionConflicts++
			if revisionConflicts >= runRevisionRetryLimit {
				return RunDeletionState{}, runRevisionRetriesExceeded("advance run deletion phase")
			}
			continue
		}
		if err != nil {
			return RunDeletionState{}, fmt.Errorf("advance run %q deletion phase to %q: %w", rootRunID, phase, err)
		}
		if updated.Deletion == nil {
			return RunDeletionState{}, fmt.Errorf("run %q deletion phase %q was not persisted", rootRunID, phase)
		}
		result := *updated.Deletion
		result.RunIDs = append([]string(nil), updated.Deletion.RunIDs...)
		return result, nil
	}
}

func collectDescendantRunIDs(ctx context.Context, store RunDeletionExecutionStore, rootID, deletionID string) ([]string, error) {
	hierarchyConflicts := 0
	for {
		result := make([]string, 0)
		visiting := map[string]bool{}
		visited := map[string]bool{}
		lineageRootID := ""
		var visit func(string, string, bool) error
		visit = func(runID, parentRunID string, requestedRoot bool) error {
			if visiting[runID] {
				return fmt.Errorf("run child lineage cycle includes %q", runID)
			}

			var run RunRecord
			reservationConflicts := 0
			for {
				loaded, err := store.GetRun(ctx, runID)
				if errors.Is(err, ErrRunnerRecordNotFound) && !requestedRoot {
					visited[runID] = true
					result = append(result, runID)
					return nil
				}
				if err != nil {
					return err
				}
				if loaded.RunID != runID {
					return fmt.Errorf("run %q hierarchy lookup returned run %q", runID, loaded.RunID)
				}
				if err := validateRunChildState(loaded); err != nil {
					return fmt.Errorf("run %q child state: %w", runID, err)
				}
				if requestedRoot {
					lineageRootID = loaded.RootRunID
					if err := validateRunnerStorageID("root run ID", lineageRootID); err != nil {
						return fmt.Errorf("run %q has invalid root lineage: %w", runID, err)
					}
					if loaded.ParentRunID == "" && lineageRootID != runID {
						return fmt.Errorf("root run %q declares root run %q", runID, lineageRootID)
					}
				} else {
					if loaded.ParentRunID != parentRunID {
						return fmt.Errorf("run %q declares parent %q, expected %q", runID, loaded.ParentRunID, parentRunID)
					}
					if loaded.RootRunID != lineageRootID {
						return fmt.Errorf("run %q declares root %q, expected %q", runID, loaded.RootRunID, lineageRootID)
					}
				}
				if loaded.Deletion != nil {
					if err := validateRunDeletionState(loaded.Deletion); err != nil {
						return fmt.Errorf("run %q deletion state: %w", runID, err)
					}
					if deletionID != "" && loaded.Deletion.ID != deletionID {
						return fmt.Errorf("run %q is reserved by deletion %q, not %q", runID, loaded.Deletion.ID, deletionID)
					}
				}
				if err := validateRunDeletionStatus(loaded); err != nil {
					return err
				}
				if loaded.Deletion != nil || deletionID == "" {
					run = loaded
					break
				}

				loaded.Deletion = &RunDeletionState{ID: deletionID, RootRunID: rootID, Phase: RunDeletionReserved}
				updated, err := store.CompareAndSwapRun(withRunDeletionMutation(ctx, deletionID), loaded.Revision, loaded)
				if errors.Is(err, ErrRunRevisionConflict) {
					reservationConflicts++
					if reservationConflicts >= runRevisionRetryLimit {
						return runRevisionRetriesExceeded("reserve deletion hierarchy run")
					}
					continue
				}
				if err != nil {
					return fmt.Errorf("reserve run %q while collecting deletion hierarchy: %w", runID, err)
				}
				run = updated
				break
			}

			if visited[runID] {
				return nil
			}
			visiting[runID] = true
			childRunIDs := append([]string(nil), run.ChildRunIDs...)
			linkedChildren := make(map[string]struct{}, len(childRunIDs))
			for _, childRunID := range childRunIDs {
				linkedChildren[childRunID] = struct{}{}
			}
			for _, pending := range run.PendingChildRuns {
				if err := validatePendingChildRun(pending); err != nil {
					return fmt.Errorf("run %q pending child reservation: %w", runID, err)
				}
				if pending.ParentRunID != runID {
					return fmt.Errorf("run %q pending child %q declares parent %q", runID, pending.ChildRunID, pending.ParentRunID)
				}
				if _, exists := linkedChildren[pending.ChildRunID]; exists {
					return fmt.Errorf("run %q child %q is both pending and finalized", runID, pending.ChildRunID)
				}
				linkedChildren[pending.ChildRunID] = struct{}{}
				childRunIDs = append(childRunIDs, pending.ChildRunID)
			}
			for _, childRunID := range childRunIDs {
				if err := validateRunnerStorageID("child run ID", childRunID); err != nil {
					return fmt.Errorf("run %q has invalid child lineage: %w", runID, err)
				}
				if err := visit(childRunID, runID, false); err != nil {
					return err
				}
			}
			persisted, err := store.GetRun(ctx, runID)
			if err != nil {
				return err
			}
			if persisted.Revision != run.Revision {
				return ErrRunRevisionConflict
			}
			delete(visiting, runID)
			visited[runID] = true
			result = append(result, runID)
			return nil
		}
		if err := visit(rootID, "", true); errors.Is(err, ErrRunRevisionConflict) {
			hierarchyConflicts++
			if hierarchyConflicts >= runRevisionRetryLimit {
				return nil, runRevisionRetriesExceeded("collect deletion hierarchy")
			}
			continue
		} else if err != nil {
			return nil, err
		}
		return result, nil
	}
}

func validateRunDeletionStatus(run RunRecord) error {
	if isActiveDeleteRunStatus(run.Status) {
		return fmt.Errorf("%w: run %q status %q must be stopped before deletion", ErrRunControlNotAllowed, run.RunID, run.Status)
	}
	if !isTerminalRunStatus(run.Status) && run.Status != RunStatusPaused {
		return fmt.Errorf("run %q has unsupported deletion status %q", run.RunID, run.Status)
	}
	return nil
}

func validateRunDeletionState(deletion *RunDeletionState) error {
	if deletion == nil {
		return nil
	}
	if err := validateRunnerStorageID("deletion ID", deletion.ID); err != nil {
		return err
	}
	if err := validateRunnerStorageID("deletion root run ID", deletion.RootRunID); err != nil {
		return err
	}
	if deletion.ParentRunID != "" {
		if err := validateRunnerStorageID("deletion parent run ID", deletion.ParentRunID); err != nil {
			return err
		}
		if deletion.ParentRunID == deletion.RootRunID {
			return fmt.Errorf("deletion root run %q cannot be its own parent", deletion.RootRunID)
		}
	}
	for _, runID := range deletion.RunIDs {
		if err := validateRunnerStorageID("deletion run ID", runID); err != nil {
			return err
		}
	}
	switch deletion.Phase {
	case RunDeletionReserved:
		if len(deletion.RunIDs) != 0 {
			return fmt.Errorf("reserved deletion %q cannot contain a run plan", deletion.ID)
		}
		return nil
	case RunDeletionPlanned, RunDeletionUnlinked:
		return validateRunDeletionPlan(deletion.RootRunID, deletion.RunIDs)
	default:
		return fmt.Errorf("deletion %q has unsupported phase %q", deletion.ID, deletion.Phase)
	}
}

func validateRunDeletionPlan(rootID string, runIDs []string) error {
	if len(runIDs) == 0 || runIDs[len(runIDs)-1] != rootID {
		return fmt.Errorf("run %q deletion plan must end with the requested run", rootID)
	}
	seen := make(map[string]struct{}, len(runIDs))
	for _, runID := range runIDs {
		if err := validateRunnerStorageID("deletion run ID", runID); err != nil {
			return err
		}
		if _, exists := seen[runID]; exists {
			return fmt.Errorf("run %q deletion plan contains duplicate run %q", rootID, runID)
		}
		seen[runID] = struct{}{}
	}
	return nil
}

func validateNewRunDeletion(run RunRecord) error {
	if run.Deletion != nil {
		return fmt.Errorf("new run %q cannot start with deletion state", run.RunID)
	}
	return nil
}

func validateNewRunParent(run, parent RunRecord) error {
	if run.ParentRunID == "" {
		return nil
	}
	if err := validateRunnerStorageID("parent run ID", run.ParentRunID); err != nil {
		return err
	}
	if parent.RunID != run.ParentRunID {
		return fmt.Errorf("child run %q parent lookup returned run %q, want %q", run.RunID, parent.RunID, run.ParentRunID)
	}
	if err := ensureRunNotDeleting(parent, "create a child run"); err != nil {
		return err
	}
	if run.RootRunID == "" || parent.RootRunID == "" || run.RootRunID != parent.RootRunID {
		return fmt.Errorf("child run %q root %q does not match parent run %q root %q", run.RunID, run.RootRunID, parent.RunID, parent.RootRunID)
	}
	if run.ChildRequestKey == "" {
		return nil
	}
	var reservation *PendingChildRun
	for _, pending := range parent.PendingChildRuns {
		if pending.ChildRunID != run.RunID {
			continue
		}
		if reservation != nil {
			return fmt.Errorf("parent run %q has duplicate reservations for child run %q", parent.RunID, run.RunID)
		}
		pendingCopy := pending
		reservation = &pendingCopy
	}
	if reservation == nil {
		return fmt.Errorf("child run %q has no pending reservation in parent run %q", run.RunID, parent.RunID)
	}
	return validateReservedChildRun(run, *reservation)
}

func validateRunChildState(run RunRecord) error {
	if run.ExecutionClaimID != "" {
		if err := validateRunnerStorageID("execution claim ID", run.ExecutionClaimID); err != nil {
			return fmt.Errorf("run %q: %w", run.RunID, err)
		}
		if strings.TrimSpace(run.ChildRequestKey) == "" {
			return fmt.Errorf("run %q execution claim requires child request identity", run.RunID)
		}
	}
	linked := make(map[string]struct{}, len(run.ChildRunIDs))
	for _, childRunID := range run.ChildRunIDs {
		if err := validateRunnerStorageID("child run ID", childRunID); err != nil {
			return fmt.Errorf("run %q: %w", run.RunID, err)
		}
		if _, exists := linked[childRunID]; exists {
			return fmt.Errorf("run %q has duplicate child run ID %q", run.RunID, childRunID)
		}
		linked[childRunID] = struct{}{}
	}
	pendingKeys := make(map[string]struct{}, len(run.PendingChildRuns))
	pendingIDs := make(map[string]struct{}, len(run.PendingChildRuns))
	for _, pending := range run.PendingChildRuns {
		if err := validatePendingChildRun(pending); err != nil {
			return fmt.Errorf("run %q pending child: %w", run.RunID, err)
		}
		if pending.ParentRunID != run.RunID {
			return fmt.Errorf("run %q pending child %q declares parent %q", run.RunID, pending.ChildRunID, pending.ParentRunID)
		}
		if _, exists := pendingKeys[pending.RequestKey]; exists {
			return fmt.Errorf("run %q has duplicate pending child request key %q", run.RunID, pending.RequestKey)
		}
		pendingKeys[pending.RequestKey] = struct{}{}
		if _, exists := pendingIDs[pending.ChildRunID]; exists {
			return fmt.Errorf("run %q has duplicate pending child run ID %q", run.RunID, pending.ChildRunID)
		}
		pendingIDs[pending.ChildRunID] = struct{}{}
		if _, exists := linked[pending.ChildRunID]; exists {
			return fmt.Errorf("run %q child %q is both pending and finalized", run.RunID, pending.ChildRunID)
		}
	}
	if run.ChildRequestKey == "" && run.ChildInputHash == "" {
		return nil
	}
	if strings.TrimSpace(run.ChildRequestKey) == "" || strings.TrimSpace(run.ChildInputHash) == "" {
		return fmt.Errorf("child run %q requires both request key and input hash", run.RunID)
	}
	if run.ChildRequestKey != strings.TrimSpace(run.ChildRequestKey) {
		return fmt.Errorf("child run %q request key cannot contain surrounding whitespace", run.RunID)
	}
	if run.ParentRunID == "" || run.ParentStepID == "" || run.ParentTaskID == "" {
		return fmt.Errorf("child run %q request identity requires parent run, step, and task IDs", run.RunID)
	}
	return validateChildRunRecordIdentity(run)
}

func validateRunDeletionTransition(ctx context.Context, existing, next RunRecord) error {
	if ctx != nil {
		if unlink, ok := ctx.Value(runDeletionUnlinkMutationKey{}).(runDeletionUnlinkMutation); ok {
			return validateRunDeletionUnlinkTransition(existing, next, unlink)
		}
	}
	existingID := ""
	if existing.Deletion != nil {
		if err := validateRunDeletionState(existing.Deletion); err != nil {
			return fmt.Errorf("run %q existing deletion state: %w", existing.RunID, err)
		}
		existingID = existing.Deletion.ID
	}
	nextID := ""
	if next.Deletion != nil {
		if err := validateRunDeletionState(next.Deletion); err != nil {
			return fmt.Errorf("run %q next deletion state: %w", next.RunID, err)
		}
		nextID = next.Deletion.ID
	}
	if existingID == "" && nextID == "" {
		return nil
	}
	mutationID := ""
	if ctx != nil {
		mutationID, _ = ctx.Value(runDeletionMutationKey{}).(string)
	}
	if mutationID == "" || nextID != mutationID || existingID != "" && existingID != mutationID {
		return fmt.Errorf("%w: run %q is reserved for deletion", ErrRunControlNotAllowed, existing.RunID)
	}
	if existingID == "" {
		if err := validateRunDeletionStatus(existing); err != nil {
			return err
		}
		if next.Deletion.Phase != RunDeletionReserved || len(next.Deletion.RunIDs) != 0 {
			return fmt.Errorf("run %q deletion must begin in phase %q", existing.RunID, RunDeletionReserved)
		}
		if next.Deletion.RootRunID == next.RunID && next.Deletion.ParentRunID != next.ParentRunID {
			return fmt.Errorf("run %q deletion parent changed from %q to %q", existing.RunID, next.ParentRunID, next.Deletion.ParentRunID)
		}
		existingRecord := cloneRunRecord(existing)
		nextRecord := cloneRunRecord(next)
		existingRecord.Deletion = nil
		nextRecord.Deletion = nil
		if !reflect.DeepEqual(existingRecord, nextRecord) {
			return fmt.Errorf("run %q deletion reservation cannot change run data", existing.RunID)
		}
		return nil
	}
	if existing.Deletion.RootRunID != next.Deletion.RootRunID {
		return fmt.Errorf("run %q deletion root is immutable", existing.RunID)
	}
	if existing.Deletion.ParentRunID != next.Deletion.ParentRunID {
		return fmt.Errorf("run %q deletion parent is immutable", existing.RunID)
	}
	if existing.Deletion.RootRunID != existing.RunID {
		if existing.Deletion.Phase != RunDeletionReserved || next.Deletion.Phase != RunDeletionReserved || len(next.Deletion.RunIDs) != 0 {
			return fmt.Errorf("descendant run %q deletion reservation cannot advance phases", existing.RunID)
		}
	} else if next.Deletion.ParentRunID != next.ParentRunID {
		return fmt.Errorf("run %q deletion parent changed from %q to %q", existing.RunID, next.ParentRunID, next.Deletion.ParentRunID)
	}
	if len(existing.Deletion.RunIDs) > 0 && !slices.Equal(existing.Deletion.RunIDs, next.Deletion.RunIDs) {
		return fmt.Errorf("run %q deletion plan is immutable", existing.RunID)
	}
	if err := validateRunDeletionPhaseTransition(existing.Deletion.Phase, next.Deletion.Phase); err != nil {
		return fmt.Errorf("run %q deletion: %w", existing.RunID, err)
	}
	existingRecord := cloneRunRecord(existing)
	nextRecord := cloneRunRecord(next)
	existingRecord.Deletion = nil
	nextRecord.Deletion = nil
	if !reflect.DeepEqual(existingRecord, nextRecord) {
		return fmt.Errorf("run %q deletion mutation cannot change run data", existing.RunID)
	}
	return nil
}

func validateRunDeletionUnlinkTransition(existing, next RunRecord, mutation runDeletionUnlinkMutation) error {
	if mutation.deletionID == "" || mutation.childRunID == "" {
		return fmt.Errorf("run deletion unlink mutation is incomplete")
	}
	if existing.RunID != next.RunID {
		return fmt.Errorf("run deletion unlink cannot change run identity")
	}
	if existing.Deletion != nil {
		if err := validateRunDeletionState(existing.Deletion); err != nil {
			return fmt.Errorf("run %q deletion state: %w", existing.RunID, err)
		}
		if existing.Deletion.Phase != RunDeletionReserved || len(existing.Deletion.RunIDs) != 0 {
			return fmt.Errorf("run %q deletion is already planned", existing.RunID)
		}
	}
	if !reflect.DeepEqual(existing.Deletion, next.Deletion) {
		return fmt.Errorf("run %q unlink cannot change parent deletion state", existing.RunID)
	}
	wantChildren := make([]string, 0, len(existing.ChildRunIDs))
	found := false
	for _, childRunID := range existing.ChildRunIDs {
		if childRunID == mutation.childRunID {
			found = true
			continue
		}
		wantChildren = append(wantChildren, childRunID)
	}
	if !found || !slices.Equal(wantChildren, next.ChildRunIDs) {
		if found || !slices.Equal(existing.ChildRunIDs, next.ChildRunIDs) {
			return fmt.Errorf("run %q unlink must only remove child run %q", existing.RunID, mutation.childRunID)
		}
	}
	wantPending := make([]PendingChildRun, 0, len(existing.PendingChildRuns))
	for _, pending := range existing.PendingChildRuns {
		if pending.ChildRunID == mutation.childRunID {
			found = true
			continue
		}
		wantPending = append(wantPending, pending)
	}
	if !found || !slices.Equal(wantPending, next.PendingChildRuns) {
		return fmt.Errorf("run %q unlink must only remove child run %q", existing.RunID, mutation.childRunID)
	}
	existingRecord := cloneRunRecord(existing)
	nextRecord := cloneRunRecord(next)
	existingRecord.ChildRunIDs = nil
	nextRecord.ChildRunIDs = nil
	existingRecord.PendingChildRuns = nil
	nextRecord.PendingChildRuns = nil
	if !reflect.DeepEqual(existingRecord, nextRecord) {
		return fmt.Errorf("run %q unlink cannot change parent run data", existing.RunID)
	}
	return nil
}

func validateRunDeletionPhaseTransition(existing, next RunDeletionPhase) error {
	switch existing {
	case RunDeletionReserved:
		if next == RunDeletionReserved || next == RunDeletionPlanned {
			return nil
		}
	case RunDeletionPlanned:
		if next == RunDeletionPlanned || next == RunDeletionUnlinked {
			return nil
		}
	case RunDeletionUnlinked:
		if next == RunDeletionUnlinked {
			return nil
		}
	}
	return fmt.Errorf("phase cannot move from %q to %q", existing, next)
}

func withRunDeletionMutation(ctx context.Context, deletionID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, runDeletionMutationKey{}, deletionID)
}

func withRunDeletionUnlinkMutation(ctx context.Context, deletionID, childRunID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = withRunDeletionMutation(ctx, deletionID)
	return context.WithValue(ctx, runDeletionUnlinkMutationKey{}, runDeletionUnlinkMutation{
		deletionID: deletionID,
		childRunID: childRunID,
	})
}

func ensureRunNotDeleting(run RunRecord, action string) error {
	if run.Deletion == nil {
		return nil
	}
	return fmt.Errorf("%w: run %q is reserved for deletion and cannot %s", ErrRunControlNotAllowed, run.RunID, action)
}

func sameRunDeletionStore(left, right RunDeleter) bool {
	if left == nil || right == nil {
		return false
	}
	leftValue := reflect.ValueOf(left)
	rightValue := reflect.ValueOf(right)
	if leftValue.Type() != rightValue.Type() {
		return false
	}
	switch leftValue.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
		return leftValue.Pointer() == rightValue.Pointer()
	default:
		return false
	}
}
