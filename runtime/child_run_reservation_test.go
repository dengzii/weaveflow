package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/state"
)

func TestChildRunReservationConcurrentRetryReusesFixedID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewMemoryExecutionStore()
	parent := RunRecord{RunID: "parent-run", Status: RunStatusRunning, RootRunID: "parent-run", RunPath: []string{"parent-run"}, Namespace: "root"}
	if err := store.CreateRun(ctx, parent); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	fixedTime := time.Unix(100, 0)
	runners := []*GraphRunner{
		{executionStore: store, now: func() time.Time { return fixedTime }},
		{executionStore: store, now: func() time.Time { return fixedTime }},
	}
	proposals := []PendingChildRun{
		newTestPendingChildRun("request-key", fixedTime),
		newTestPendingChildRun("request-key", fixedTime),
	}

	start := make(chan struct{})
	results := make([]PendingChildRun, len(runners))
	errorsByIndex := make([]error, len(runners))
	var waitGroup sync.WaitGroup
	for index, runner := range runners {
		waitGroup.Add(1)
		go func(resultIndex int, childRunner *GraphRunner) {
			defer waitGroup.Done()
			<-start
			results[resultIndex], errorsByIndex[resultIndex] = childRunner.reserveChildRun(ctx, parent.RunID, proposals[resultIndex])
		}(index, runner)
	}
	close(start)
	waitGroup.Wait()

	for index, reserveErr := range errorsByIndex {
		if reserveErr != nil {
			t.Fatalf("reserveChildRun() call %d error = %v", index, reserveErr)
		}
	}
	if results[0].ChildRunID != results[1].ChildRunID {
		t.Fatalf("reserved child run IDs = %q and %q", results[0].ChildRunID, results[1].ChildRunID)
	}
	persisted, err := store.GetRun(ctx, parent.RunID)
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if len(persisted.PendingChildRuns) != 1 || persisted.PendingChildRuns[0].ChildRunID != results[0].ChildRunID {
		t.Fatalf("pending child reservations = %#v", persisted.PendingChildRuns)
	}
}

func TestChildRunCreatedBeforeFinalizeIsRecovered(t *testing.T) {
	t.Parallel()

	fixture := newChildRunRecoveryFixture(t, false)
	assertChildRunRecovery(t, fixture)
}

func TestChildRunFinalizedRetryDoesNotCreateSecondRun(t *testing.T) {
	t.Parallel()

	fixture := newChildRunRecoveryFixture(t, true)
	assertChildRunRecovery(t, fixture)
}

func TestChildRunFinalizedIdentityMismatchDoesNotCreatePending(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(*childRunRecoveryFixture)
		want   string
	}{
		{
			name: "input",
			change: func(fixture *childRunRecoveryFixture) {
				fixture.input = state.FromShared(map[string]any{"changed": true})
			},
			want: "input hash",
		},
		{
			name: "graph",
			change: func(fixture *childRunRecoveryFixture) {
				fixture.runner.graphID = "changed-graph"
			},
			want: "graph ID",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newChildRunRecoveryFixture(t, true)
			testCase.change(&fixture)
			_, err := fixture.runner.RunChild(fixture.ctx, fixture.request, fixture.input)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("RunChild() error = %v, want %q", err, testCase.want)
			}
			parent, err := fixture.store.GetRun(context.Background(), fixture.parentID)
			if err != nil {
				t.Fatalf("GetRun(parent) error = %v", err)
			}
			if len(parent.PendingChildRuns) != 0 {
				t.Fatalf("identity mismatch persisted pending children = %#v", parent.PendingChildRuns)
			}
			if len(parent.ChildRunIDs) != 1 || parent.ChildRunIDs[0] != fixture.childID {
				t.Fatalf("identity mismatch changed child links = %#v", parent.ChildRunIDs)
			}
		})
	}
}

func TestFinalizeChildRunClearsPendingAndRejectsCorruption(t *testing.T) {
	t.Parallel()

	fixedTime := time.Unix(200, 0)
	reservation := newTestPendingChildRun("child-run", fixedTime)
	parent := RunRecord{
		RunID:            reservation.ParentRunID,
		Status:           RunStatusRunning,
		ChildRunIDs:      []string{"other-child"},
		PendingChildRuns: []PendingChildRun{reservation},
	}
	changed, err := finalizePendingChildRun(&parent, reservation.RequestKey, reservation.ChildRunID, fixedTime.Add(time.Second))
	if err != nil {
		t.Fatalf("finalizePendingChildRun() error = %v", err)
	}
	if !changed || len(parent.PendingChildRuns) != 0 {
		t.Fatalf("finalized parent = %#v", parent)
	}
	if len(parent.ChildRunIDs) != 2 || parent.ChildRunIDs[1] != reservation.ChildRunID {
		t.Fatalf("child run links = %#v", parent.ChildRunIDs)
	}
	changed, err = finalizePendingChildRun(&parent, reservation.RequestKey, reservation.ChildRunID, fixedTime.Add(2*time.Second))
	if err != nil || changed {
		t.Fatalf("idempotent finalize = %v, %v", changed, err)
	}

	corruptions := []struct {
		name   string
		parent RunRecord
		want   string
	}{
		{
			name: "pending child ID mismatch",
			parent: RunRecord{RunID: reservation.ParentRunID, PendingChildRuns: []PendingChildRun{
				reservation,
			}},
			want: "reserves run",
		},
		{
			name:   "missing reservation and link",
			parent: RunRecord{RunID: reservation.ParentRunID},
			want:   "no pending reservation or finalized link",
		},
		{
			name:   "duplicate finalized link",
			parent: RunRecord{RunID: reservation.ParentRunID, ChildRunIDs: []string{reservation.ChildRunID, reservation.ChildRunID}},
			want:   "duplicate child run ID",
		},
		{
			name: "duplicate pending request",
			parent: RunRecord{RunID: reservation.ParentRunID, PendingChildRuns: []PendingChildRun{
				reservation,
				reservation,
			}},
			want: "duplicate pending child request key",
		},
	}
	for _, testCase := range corruptions {
		t.Run(testCase.name, func(t *testing.T) {
			childRunID := reservation.ChildRunID
			if testCase.name == "pending child ID mismatch" {
				childRunID = "different-child"
			}
			_, finalizeErr := finalizePendingChildRun(&testCase.parent, reservation.RequestKey, childRunID, fixedTime)
			if finalizeErr == nil || !strings.Contains(finalizeErr.Error(), testCase.want) {
				t.Fatalf("finalizePendingChildRun() error = %v, want %q", finalizeErr, testCase.want)
			}
		})
	}
}

func TestChildRunReservationRejectsChangedInputOrGraphIdentity(t *testing.T) {
	t.Parallel()

	fixedTime := time.Unix(300, 0)
	reservation := newTestPendingChildRun("child-run", fixedTime)
	tests := []struct {
		name   string
		change func(*PendingChildRun)
		want   string
	}{
		{name: "input", change: func(pending *PendingChildRun) { pending.InputHash = "changed-input" }, want: "input hash changed"},
		{name: "graph", change: func(pending *PendingChildRun) { pending.GraphID = "changed-graph" }, want: "graph ID changed"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			parent := RunRecord{
				RunID:            reservation.ParentRunID,
				Status:           RunStatusRunning,
				PendingChildRuns: []PendingChildRun{reservation},
			}
			proposed := reservation
			testCase.change(&proposed)
			_, _, err := reservePendingChildRun(&parent, proposed, fixedTime.Add(time.Second))
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("reservePendingChildRun() error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestChildRunReservationReusesOriginalStepAcrossResumeAttempts(t *testing.T) {
	t.Parallel()

	fixedTime := time.Unix(310, 0)
	reservation := newTestPendingChildRun("child-run-resume", fixedTime)
	parent := RunRecord{
		RunID:            reservation.ParentRunID,
		Status:           RunStatusRunning,
		PendingChildRuns: []PendingChildRun{reservation},
	}
	proposed := reservation
	proposed.ParentStepID = "resumed-step"

	reused, changed, err := reservePendingChildRun(&parent, proposed, fixedTime.Add(time.Second))
	if err != nil {
		t.Fatalf("reservePendingChildRun() error = %v", err)
	}
	if changed {
		t.Fatal("reservePendingChildRun() rewrote an existing logical task reservation")
	}
	if reused.ParentStepID != reservation.ParentStepID {
		t.Fatalf("reused parent step ID = %q, want original %q", reused.ParentStepID, reservation.ParentStepID)
	}
	if len(parent.PendingChildRuns) != 1 || parent.PendingChildRuns[0].ParentStepID != reservation.ParentStepID {
		t.Fatalf("persisted reservations = %#v, want original lineage", parent.PendingChildRuns)
	}
}

func TestChildRunRequestKeyEncodingIsUnambiguous(t *testing.T) {
	t.Parallel()

	left := ChildRunRequest{ParentRunID: "parent", ParentTaskID: "task", GraphRef: "a\x00b", Namespace: "c"}
	right := ChildRunRequest{ParentRunID: "parent", ParentTaskID: "task", GraphRef: "a", Namespace: "b\x00c"}
	if leftKey, rightKey := childRunRequestKey(left), childRunRequestKey(right); leftKey == rightKey {
		t.Fatalf("ambiguous child request keys = %q", leftKey)
	}
}

func TestChildRunReservationRequiresDeterministicRunID(t *testing.T) {
	t.Parallel()

	reservation := newTestPendingChildRun("request-key", time.Unix(325, 0))
	reservation.ChildRunID = "different-child"
	if err := validatePendingChildRun(reservation); err == nil || !strings.Contains(err.Error(), "does not match request key") {
		t.Fatalf("validatePendingChildRun() error = %v", err)
	}
	run := RunRecord{RunID: "different-child", ChildRequestKey: reservation.RequestKey}
	if err := validateChildRunRecordIdentity(run); err == nil || !strings.Contains(err.Error(), "does not match request key") {
		t.Fatalf("validateChildRunRecordIdentity() error = %v", err)
	}
}

func TestChildRunStoreRejectsInvalidIdentityUpdate(t *testing.T) {
	t.Parallel()

	fixture := newChildRunRecoveryFixture(t, true)
	tests := []struct {
		name   string
		change func(*RunRecord)
		want   string
	}{
		{name: "request key", change: func(run *RunRecord) { run.ChildRequestKey = "different-request-key" }, want: "does not match request key"},
		{name: "execution claim", change: func(run *RunRecord) { run.ExecutionClaimID = "not/portable" }, want: "portable record ID"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			child, err := fixture.store.GetRun(context.Background(), fixture.childID)
			if err != nil {
				t.Fatalf("GetRun(child) error = %v", err)
			}
			testCase.change(&child)
			if _, err := fixture.store.CompareAndSwapRun(context.Background(), child.Revision, child); err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("CompareAndSwapRun() error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestChildRunReservationRejectsDeletingParent(t *testing.T) {
	t.Parallel()

	fixedTime := time.Unix(350, 0)
	proposed := newTestPendingChildRun("child-run", fixedTime)
	parent := RunRecord{
		RunID: proposed.ParentRunID, Status: RunStatusRunning,
		Deletion: &RunDeletionState{ID: "deletion", RootRunID: proposed.ParentRunID, Phase: RunDeletionReserved},
	}
	_, changed, err := reservePendingChildRun(&parent, proposed, fixedTime)
	if err == nil || !strings.Contains(err.Error(), "reserved for deletion") {
		t.Fatalf("reservePendingChildRun() error = %v", err)
	}
	if changed || len(parent.PendingChildRuns) != 0 {
		t.Fatalf("deleting parent changed = %v, pending = %#v", changed, parent.PendingChildRuns)
	}
}

func TestChildRunExecutionClaimAllowsOneRunner(t *testing.T) {
	t.Parallel()

	fixture := newChildRunFixture(t, true, RunStatusRunning, "")
	runners := []*GraphRunner{
		fixture.runner,
		{executionStore: fixture.store, now: fixture.runner.now},
	}
	claimedRuns := make([]RunRecord, len(runners))
	claimIDs := make([]string, len(runners))
	errorsByIndex := make([]error, len(runners))
	start := make(chan struct{})
	var waitGroup sync.WaitGroup
	for index, runner := range runners {
		waitGroup.Add(1)
		go func(resultIndex int, childRunner *GraphRunner) {
			defer waitGroup.Done()
			<-start
			claimedRuns[resultIndex], claimIDs[resultIndex], errorsByIndex[resultIndex] = childRunner.claimChildRunExecution(context.Background(), fixture.childID)
		}(index, runner)
	}
	close(start)
	waitGroup.Wait()

	successIndex := -1
	for index, claimErr := range errorsByIndex {
		if claimErr == nil {
			if successIndex >= 0 {
				t.Fatalf("multiple execution claims succeeded: %q and %q", claimIDs[successIndex], claimIDs[index])
			}
			successIndex = index
			continue
		}
		if !errors.Is(claimErr, ErrRunControlNotAllowed) {
			t.Fatalf("claimChildRunExecution() call %d error = %v", index, claimErr)
		}
	}
	if successIndex < 0 || claimedRuns[successIndex].ExecutionClaimID != claimIDs[successIndex] {
		t.Fatalf("successful execution claim = %#v, %q", claimedRuns, claimIDs)
	}
	if err := runners[successIndex].releaseChildRunExecution(context.Background(), fixture.childID, claimIDs[successIndex]); err != nil {
		t.Fatalf("releaseChildRunExecution() error = %v", err)
	}
	persisted, err := fixture.store.GetRun(context.Background(), fixture.childID)
	if err != nil {
		t.Fatalf("GetRun(child) error = %v", err)
	}
	if persisted.ExecutionClaimID != "" {
		t.Fatalf("persisted execution claim = %q", persisted.ExecutionClaimID)
	}
}

func TestConcurrentChildRunRetryExecutesOnceAcrossRunners(t *testing.T) {
	t.Parallel()

	fixture := newChildRunFixture(t, true, RunStatusRunning, "")
	checkpointStore := NewMemoryCheckpointStore()
	eventSink := NewMemoryEventSink()
	transactionStore, err := resolveRuntimeTransactionStore(fixture.store, checkpointStore, eventSink)
	if err != nil {
		t.Fatalf("resolveRuntimeTransactionStore() error = %v", err)
	}
	workflow := &blockingChildRunGraph{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	newRunner := func() *GraphRunner {
		return newTestChildExecutionRunner(fixture, workflow, checkpointStore, eventSink, transactionStore)
	}
	firstResults := make(chan ChildRunResult, 1)
	firstErrors := make(chan error, 1)
	go func() {
		result, runErr := newRunner().RunChild(fixture.ctx, fixture.request, fixture.input)
		firstResults <- result
		firstErrors <- runErr
	}()
	select {
	case <-workflow.entered:
	case <-time.After(5 * time.Second):
		close(workflow.release)
		t.Fatal("first child run did not enter execution")
	}

	secondResults := make(chan ChildRunResult, 1)
	secondErrors := make(chan error, 1)
	go func() {
		result, runErr := newRunner().RunChild(fixture.ctx, fixture.request, fixture.input)
		secondResults <- result
		secondErrors <- runErr
	}()
	var secondErr error
	select {
	case secondErr = <-secondErrors:
	case <-time.After(5 * time.Second):
		close(workflow.release)
		t.Fatal("concurrent child run retry did not return")
	}
	if !errors.Is(secondErr, ErrRunControlNotAllowed) {
		close(workflow.release)
		t.Fatalf("concurrent RunChild() error = %v", secondErr)
	}
	<-secondResults
	close(workflow.release)
	if firstErr := <-firstErrors; firstErr != nil {
		t.Fatalf("first RunChild() error = %v", firstErr)
	}
	firstResult := <-firstResults
	if firstResult.Run.Status != RunStatusCompleted {
		t.Fatalf("first RunChild() status = %q", firstResult.Run.Status)
	}
	if invocationCount := workflow.invocations.Load(); invocationCount != 1 {
		t.Fatalf("child runnable invocation count = %d, want 1", invocationCount)
	}
	persisted, err := fixture.store.GetRun(context.Background(), fixture.childID)
	if err != nil {
		t.Fatalf("GetRun(child) error = %v", err)
	}
	if persisted.ExecutionClaimID != "" {
		t.Fatalf("persisted execution claim after completion = %q", persisted.ExecutionClaimID)
	}
}

func TestRecoverChildRunExplicitlyTakesOverAbandonedClaim(t *testing.T) {
	t.Parallel()

	fixture := newChildRunFixture(t, true, RunStatusRunning, "")
	child, err := fixture.store.GetRun(context.Background(), fixture.childID)
	if err != nil {
		t.Fatalf("GetRun(child) error = %v", err)
	}
	const abandonedClaimID = "abandoned-claim"
	child.ExecutionClaimID = abandonedClaimID
	if _, err := fixture.store.CompareAndSwapRun(context.Background(), child.Revision, child); err != nil {
		t.Fatalf("CompareAndSwapRun(child) error = %v", err)
	}
	checkpointStore := NewMemoryCheckpointStore()
	eventSink := NewMemoryEventSink()
	transactionStore, err := resolveRuntimeTransactionStore(fixture.store, checkpointStore, eventSink)
	if err != nil {
		t.Fatalf("resolveRuntimeTransactionStore() error = %v", err)
	}
	workflow := &blockingChildRunGraph{entered: make(chan struct{}, 1), release: make(chan struct{})}
	runner := newTestChildExecutionRunner(fixture, workflow, checkpointStore, eventSink, transactionStore)
	if _, err := runner.RunChild(fixture.ctx, fixture.request, fixture.input); !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("RunChild() with abandoned claim error = %v", err)
	}
	if _, err := runner.RecoverChildRun(fixture.ctx, fixture.request, fixture.input, "wrong-claim"); err == nil || !strings.Contains(err.Error(), "not abandoned claim") {
		t.Fatalf("RecoverChildRun() wrong claim error = %v", err)
	}

	recoveryResults := make(chan ChildRunResult, 1)
	recoveryErrors := make(chan error, 1)
	go func() {
		result, recoverErr := runner.RecoverChildRun(fixture.ctx, fixture.request, fixture.input, abandonedClaimID)
		recoveryResults <- result
		recoveryErrors <- recoverErr
	}()
	select {
	case <-workflow.entered:
	case <-time.After(5 * time.Second):
		close(workflow.release)
		t.Fatal("recovered child run did not enter execution")
	}
	claimed, err := fixture.store.GetRun(context.Background(), fixture.childID)
	if err != nil {
		close(workflow.release)
		t.Fatalf("GetRun(claimed child) error = %v", err)
	}
	if claimed.ExecutionClaimID == "" || claimed.ExecutionClaimID == abandonedClaimID {
		close(workflow.release)
		t.Fatalf("takeover execution claim = %q", claimed.ExecutionClaimID)
	}
	close(workflow.release)
	if recoverErr := <-recoveryErrors; recoverErr != nil {
		t.Fatalf("RecoverChildRun() error = %v", recoverErr)
	}
	result := <-recoveryResults
	if result.Run.Status != RunStatusCompleted {
		t.Fatalf("RecoverChildRun() status = %q", result.Run.Status)
	}
	persisted, err := fixture.store.GetRun(context.Background(), fixture.childID)
	if err != nil {
		t.Fatalf("GetRun(completed child) error = %v", err)
	}
	if persisted.ExecutionClaimID != "" {
		t.Fatalf("completed child execution claim = %q", persisted.ExecutionClaimID)
	}
}

func TestChildRunOldOwnerCannotPersistAfterTakeover(t *testing.T) {
	t.Parallel()

	fixture := newChildRunFixture(t, true, RunStatusRunning, "")
	claimed, oldClaimID, err := fixture.runner.claimChildRunExecution(context.Background(), fixture.childID)
	if err != nil {
		t.Fatalf("claimChildRunExecution() error = %v", err)
	}
	oldOwnerCtx := context.WithValue(context.Background(), childRunExecutionOwnerKey{}, oldClaimID)
	execution := &graphRunnerExecution{runner: fixture.runner, run: claimed}
	takeoverCtx := context.WithValue(context.Background(), childRunExecutionTakeoverKey{}, oldClaimID)
	_, newClaimID, err := fixture.runner.claimChildRunExecution(takeoverCtx, fixture.childID)
	if err != nil {
		t.Fatalf("claimChildRunExecution(takeover) error = %v", err)
	}
	_, _, err = execution.persistRunChecked(oldOwnerCtx, func(run *RunRecord) (bool, error) {
		run.ErrorMessage = "stale owner write"
		return true, nil
	})
	if !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("persistRunChecked() stale owner error = %v", err)
	}
	persisted, err := fixture.store.GetRun(context.Background(), fixture.childID)
	if err != nil {
		t.Fatalf("GetRun(child) error = %v", err)
	}
	if persisted.ExecutionClaimID != newClaimID || persisted.ErrorMessage == "stale owner write" {
		t.Fatalf("persisted child after stale write = %#v", persisted)
	}
	if err := fixture.runner.releaseChildRunExecution(context.Background(), fixture.childID, newClaimID); err != nil {
		t.Fatalf("releaseChildRunExecution() error = %v", err)
	}
}

func TestChildRunCheckpointOnlyCommitRejectsStaleOwner(t *testing.T) {
	t.Parallel()

	fixture := newChildRunFixture(t, true, RunStatusRunning, "")
	checkpointStore := NewMemoryCheckpointStore()
	eventSink := NewMemoryEventSink()
	transactionStore, err := resolveRuntimeTransactionStore(fixture.store, checkpointStore, eventSink)
	if err != nil {
		t.Fatalf("resolveRuntimeTransactionStore() error = %v", err)
	}
	runner := newTestChildExecutionRunner(fixture, &blockingChildRunGraph{}, checkpointStore, eventSink, transactionStore)
	_, oldClaimID, err := runner.claimChildRunExecution(context.Background(), fixture.childID)
	if err != nil {
		t.Fatalf("claimChildRunExecution() error = %v", err)
	}
	oldOwnerCtx := context.WithValue(context.Background(), childRunExecutionOwnerKey{}, oldClaimID)
	takeoverCtx := context.WithValue(context.Background(), childRunExecutionTakeoverKey{}, oldClaimID)
	_, newClaimID, err := runner.claimChildRunExecution(takeoverCtx, fixture.childID)
	if err != nil {
		t.Fatalf("claimChildRunExecution(takeover) error = %v", err)
	}
	_, err = runner.commitRuntime(oldOwnerCtx, Commit{Checkpoints: []CheckpointWrite{{
		Record: CheckpointRecord{CheckpointID: "stale-checkpoint", RunID: fixture.childID},
	}}})
	if !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("commitRuntime() stale checkpoint owner error = %v", err)
	}
	if _, _, err := checkpointStore.Load(context.Background(), "stale-checkpoint"); err == nil {
		t.Fatal("stale checkpoint was persisted")
	}
	if err := runner.releaseChildRunExecution(context.Background(), fixture.childID, newClaimID); err != nil {
		t.Fatalf("releaseChildRunExecution() error = %v", err)
	}
}

func TestChildRunCheckpointOnlyCommitPreservesRevisionForPause(t *testing.T) {
	t.Parallel()

	fixture := newChildRunFixture(t, true, RunStatusRunning, "")
	checkpointStore := NewMemoryCheckpointStore()
	eventSink := NewMemoryEventSink()
	transactionStore, err := resolveRuntimeTransactionStore(fixture.store, checkpointStore, eventSink)
	if err != nil {
		t.Fatalf("resolveRuntimeTransactionStore() error = %v", err)
	}
	runner := newTestChildExecutionRunner(fixture, &blockingChildRunGraph{}, checkpointStore, eventSink, transactionStore)
	claimed, claimID, err := runner.claimChildRunExecution(context.Background(), fixture.childID)
	if err != nil {
		t.Fatalf("claimChildRunExecution() error = %v", err)
	}
	ownerCtx := context.WithValue(context.Background(), childRunExecutionOwnerKey{}, claimID)
	checkpointID, _, err := runner.saveCheckpoint(ownerCtx, claimed, StepRecord{}, "start", CheckpointBeforeNode, fixture.input, 0, nil, nil)
	if err != nil {
		t.Fatalf("saveCheckpoint() error = %v", err)
	}
	afterCheckpoint, err := fixture.store.GetRun(context.Background(), fixture.childID)
	if err != nil {
		t.Fatalf("GetRun(after checkpoint) error = %v", err)
	}
	if afterCheckpoint.Revision != claimed.Revision {
		t.Fatalf("checkpoint-only commit revision = %d, want %d", afterCheckpoint.Revision, claimed.Revision)
	}
	paused, _, err := runner.pauseRunAtCheckpoint(ownerCtx, claimed, fixture.input, checkpointID, nil, "")
	if err != nil {
		t.Fatalf("pauseRunAtCheckpoint() error = %v", err)
	}
	if paused.Status != RunStatusPaused || paused.Revision != claimed.Revision+1 {
		t.Fatalf("paused run = %#v", paused)
	}
	if err := runner.releaseChildRunExecution(context.Background(), fixture.childID, claimID); err != nil {
		t.Fatalf("releaseChildRunExecution() error = %v", err)
	}
}

func TestChildRunCheckpointGuardRetriesUnrelatedRevisionConflict(t *testing.T) {
	t.Parallel()

	fixture := newChildRunFixture(t, true, RunStatusRunning, "")
	checkpointStore := NewMemoryCheckpointStore()
	eventSink := NewMemoryEventSink()
	baseStore, err := resolveRuntimeTransactionStore(fixture.store, checkpointStore, eventSink)
	if err != nil {
		t.Fatalf("resolveRuntimeTransactionStore() error = %v", err)
	}
	hookStore := &runCheckHookTransactionStore{TransactionStore: baseStore}
	runner := newTestChildExecutionRunner(fixture, &blockingChildRunGraph{}, checkpointStore, eventSink, hookStore)
	claimed, claimID, err := runner.claimChildRunExecution(context.Background(), fixture.childID)
	if err != nil {
		t.Fatalf("claimChildRunExecution() error = %v", err)
	}
	hookStore.beforeFirstCheck = func(ctx context.Context) error {
		persisted, loadErr := fixture.store.GetRun(ctx, fixture.childID)
		if loadErr != nil {
			return loadErr
		}
		if persisted.ExecutionClaimID != claimID {
			return fmt.Errorf("execution claim changed to %q", persisted.ExecutionClaimID)
		}
		persisted.PauseRequested = true
		_, swapErr := fixture.store.CompareAndSwapRun(ctx, persisted.Revision, persisted)
		return swapErr
	}
	ownerCtx := context.WithValue(context.Background(), childRunExecutionOwnerKey{}, claimID)
	checkpointID, _, err := runner.saveCheckpoint(ownerCtx, claimed, StepRecord{}, "start", CheckpointBeforeNode, fixture.input, 0, nil, nil)
	if err != nil {
		t.Fatalf("saveCheckpoint() error = %v", err)
	}
	if attempts := hookStore.checkAttempts.Load(); attempts != 2 {
		t.Fatalf("run check attempts = %d, want 2", attempts)
	}
	restored, err := runner.LoadCheckpointState(context.Background(), checkpointID)
	if err != nil {
		t.Fatalf("LoadCheckpointState() error = %v", err)
	}
	if !restored.Runtime.PauseRequested {
		t.Fatalf("checkpoint pause requested = false, want true")
	}
	persisted, err := fixture.store.GetRun(context.Background(), fixture.childID)
	if err != nil {
		t.Fatalf("GetRun(child) error = %v", err)
	}
	if !persisted.PauseRequested || persisted.ExecutionClaimID != claimID || persisted.Revision != claimed.Revision+1 {
		t.Fatalf("persisted child after retry = %#v", persisted)
	}
	if err := runner.releaseChildRunExecution(context.Background(), fixture.childID, claimID); err != nil {
		t.Fatalf("releaseChildRunExecution() error = %v", err)
	}
}

func TestChildRunCheckpointGuardUsesLatestControlStateOnFirstAttempt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		setControl func(*RunRecord)
		wantPause  bool
		wantCancel bool
	}{
		{name: "pause", setControl: func(run *RunRecord) { run.PauseRequested = true }, wantPause: true},
		{name: "cancel", setControl: func(run *RunRecord) { run.CancelRequested = true }, wantCancel: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newChildRunFixture(t, true, RunStatusRunning, "")
			checkpointStore := NewMemoryCheckpointStore()
			eventSink := NewMemoryEventSink()
			baseStore, err := resolveRuntimeTransactionStore(fixture.store, checkpointStore, eventSink)
			if err != nil {
				t.Fatalf("resolveRuntimeTransactionStore() error = %v", err)
			}
			hookStore := &runCheckHookTransactionStore{TransactionStore: baseStore}
			runner := newTestChildExecutionRunner(fixture, &blockingChildRunGraph{}, checkpointStore, eventSink, hookStore)
			claimed, claimID, err := runner.claimChildRunExecution(context.Background(), fixture.childID)
			if err != nil {
				t.Fatalf("claimChildRunExecution() error = %v", err)
			}
			persisted, err := fixture.store.GetRun(context.Background(), fixture.childID)
			if err != nil {
				t.Fatalf("GetRun(child) error = %v", err)
			}
			testCase.setControl(&persisted)
			updated, err := fixture.store.CompareAndSwapRun(context.Background(), persisted.Revision, persisted)
			if err != nil {
				t.Fatalf("CompareAndSwapRun(control) error = %v", err)
			}

			ownerCtx := context.WithValue(context.Background(), childRunExecutionOwnerKey{}, claimID)
			checkpointID, checkpointRun, err := runner.saveCheckpoint(ownerCtx, claimed, StepRecord{}, "start", CheckpointBeforeNode, fixture.input, 0, nil, nil)
			if err != nil {
				t.Fatalf("saveCheckpoint() error = %v", err)
			}
			if attempts := hookStore.checkAttempts.Load(); attempts != 1 {
				t.Fatalf("run check attempts = %d, want 1", attempts)
			}
			if checkpointRun.Revision != updated.Revision || checkpointRun.PauseRequested != testCase.wantPause || checkpointRun.CancelRequested != testCase.wantCancel {
				t.Fatalf("checkpoint run = %#v, updated = %#v", checkpointRun, updated)
			}
			restored, err := runner.LoadCheckpointState(context.Background(), checkpointID)
			if err != nil {
				t.Fatalf("LoadCheckpointState() error = %v", err)
			}
			if restored.Runtime.PauseRequested != testCase.wantPause || restored.Runtime.CancelRequested != testCase.wantCancel {
				t.Fatalf("checkpoint controls = pause:%t cancel:%t", restored.Runtime.PauseRequested, restored.Runtime.CancelRequested)
			}
			if err := runner.releaseChildRunExecution(context.Background(), fixture.childID, claimID); err != nil {
				t.Fatalf("releaseChildRunExecution() error = %v", err)
			}
		})
	}
}

func TestRootRunCheckpointGuardStopsAfterRevisionRetryLimit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Unix(500, 0)
	run := RunRecord{RunID: "root-run", GraphID: "graph", Status: RunStatusRunning, StartedAt: now, UpdatedAt: now}
	executionStore := NewMemoryExecutionStore()
	if err := executionStore.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	checkpointStore := NewMemoryCheckpointStore()
	eventSink := NewMemoryEventSink()
	baseStore, err := resolveRuntimeTransactionStore(executionStore, checkpointStore, eventSink)
	if err != nil {
		t.Fatalf("resolveRuntimeTransactionStore() error = %v", err)
	}
	hookStore := &runCheckHookTransactionStore{TransactionStore: baseStore, alwaysConflict: true}
	runner := &GraphRunner{
		executionStore:   executionStore,
		checkpointStore:  checkpointStore,
		codec:            state.NewJSONStateCodec(""),
		eventSink:        eventSink,
		transactionStore: hookStore,
		now:              func() time.Time { return now },
	}
	checkpointID, _, err := runner.saveCheckpoint(ctx, run, StepRecord{}, "start", CheckpointBeforeNode, state.NewState(), 0, nil, nil)
	if !errors.Is(err, ErrRunRevisionConflict) || !strings.Contains(err.Error(), "exceeded 8") {
		t.Fatalf("saveCheckpoint() error = %v, want bounded revision conflict", err)
	}
	if checkpointID != "" {
		t.Fatalf("saveCheckpoint() ID = %q, want empty", checkpointID)
	}
	if attempts := hookStore.checkAttempts.Load(); attempts != 8 {
		t.Fatalf("run check attempts = %d, want 8", attempts)
	}
	checkpoints, err := checkpointStore.List(ctx, run.RunID)
	if err != nil {
		t.Fatalf("List(checkpoints) error = %v", err)
	}
	if len(checkpoints) != 0 {
		t.Fatalf("persisted checkpoints = %d, want 0", len(checkpoints))
	}
}

func TestRuntimeRunUpdateStopsAfterRevisionRetryLimit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Unix(501, 0)
	run := RunRecord{RunID: "root-run-update", GraphID: "graph", Status: RunStatusRunning, StartedAt: now, UpdatedAt: now}
	executionStore := NewMemoryExecutionStore()
	if err := executionStore.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	checkpointStore := NewMemoryCheckpointStore()
	eventSink := NewMemoryEventSink()
	baseStore, err := resolveRuntimeTransactionStore(executionStore, checkpointStore, eventSink)
	if err != nil {
		t.Fatalf("resolveRuntimeTransactionStore() error = %v", err)
	}
	hookStore := &runCheckHookTransactionStore{TransactionStore: baseStore, alwaysConflict: true}
	runner := &GraphRunner{executionStore: executionStore, transactionStore: hookStore, now: func() time.Time { return now }}
	execution := &graphRunnerExecution{runner: runner, run: run}
	_, _, err = execution.persistRunChecked(ctx, func(current *RunRecord) (bool, error) {
		current.ErrorMessage = "must not persist"
		return true, nil
	})
	if !errors.Is(err, ErrRunRevisionConflict) || !strings.Contains(err.Error(), "exceeded 8") {
		t.Fatalf("persistRunChecked() error = %v, want bounded revision conflict", err)
	}
	if attempts := hookStore.commitAttempts.Load(); attempts != 8 {
		t.Fatalf("commit attempts = %d, want 8", attempts)
	}
	persisted, err := executionStore.GetRun(ctx, run.RunID)
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if persisted.ErrorMessage != "" {
		t.Fatalf("persisted run error message = %q", persisted.ErrorMessage)
	}
}

func TestChildRunCheckpointGuardRejectsTakeoverBeforeCommit(t *testing.T) {
	t.Parallel()

	fixture := newChildRunFixture(t, true, RunStatusRunning, "")
	checkpointStore := NewMemoryCheckpointStore()
	eventSink := NewMemoryEventSink()
	baseStore, err := resolveRuntimeTransactionStore(fixture.store, checkpointStore, eventSink)
	if err != nil {
		t.Fatalf("resolveRuntimeTransactionStore() error = %v", err)
	}
	hookStore := &runCheckHookTransactionStore{TransactionStore: baseStore}
	runner := newTestChildExecutionRunner(fixture, &blockingChildRunGraph{}, checkpointStore, eventSink, hookStore)
	claimed, oldClaimID, err := runner.claimChildRunExecution(context.Background(), fixture.childID)
	if err != nil {
		t.Fatalf("claimChildRunExecution() error = %v", err)
	}
	newClaimID := ""
	hookStore.beforeFirstCheck = func(ctx context.Context) error {
		takeoverCtx := context.WithValue(ctx, childRunExecutionTakeoverKey{}, oldClaimID)
		_, claimedID, claimErr := runner.claimChildRunExecution(takeoverCtx, fixture.childID)
		newClaimID = claimedID
		return claimErr
	}
	ownerCtx := context.WithValue(context.Background(), childRunExecutionOwnerKey{}, oldClaimID)
	checkpointID, _, err := runner.saveCheckpoint(ownerCtx, claimed, StepRecord{}, "start", CheckpointBeforeNode, fixture.input, 0, nil, nil)
	if !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("saveCheckpoint() takeover error = %v", err)
	}
	if attempts := hookStore.checkAttempts.Load(); attempts != 1 {
		t.Fatalf("run check attempts = %d, want 1", attempts)
	}
	if checkpointID != "" {
		t.Fatalf("saveCheckpoint() ID = %q, want empty", checkpointID)
	}
	persisted, err := fixture.store.GetRun(context.Background(), fixture.childID)
	if err != nil {
		t.Fatalf("GetRun(child) error = %v", err)
	}
	if newClaimID == "" || persisted.ExecutionClaimID != newClaimID {
		t.Fatalf("persisted takeover claim = %q, want %q", persisted.ExecutionClaimID, newClaimID)
	}
	if err := runner.releaseChildRunExecution(context.Background(), fixture.childID, newClaimID); err != nil {
		t.Fatalf("releaseChildRunExecution() error = %v", err)
	}
}

func TestChildRunNodeExecutionRejectsLostOwnershipAndInactiveStatus(t *testing.T) {
	t.Parallel()

	base := RunRecord{RunID: "child-run", Status: RunStatusRunning, ExecutionClaimID: "claim"}
	tests := []struct {
		name      string
		persisted RunRecord
		want      string
	}{
		{name: "lost claim", persisted: RunRecord{RunID: base.RunID, Status: RunStatusRunning, ExecutionClaimID: "other-claim"}, want: "execution claim changed"},
		{name: "paused", persisted: RunRecord{RunID: base.RunID, Status: RunStatusPaused, ExecutionClaimID: base.ExecutionClaimID}, want: "cannot execute a node"},
		{name: "terminal", persisted: RunRecord{RunID: base.RunID, Status: RunStatusCompleted, ExecutionClaimID: base.ExecutionClaimID}, want: "cannot execute a node"},
		{name: "deleting", persisted: RunRecord{RunID: base.RunID, Status: RunStatusRunning, ExecutionClaimID: base.ExecutionClaimID, Deletion: &RunDeletionState{ID: "deletion"}}, want: "reserved for deletion"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateNodeExecutionRun(base, testCase.persisted)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("validateNodeExecutionRun() error = %v, want %q", err, testCase.want)
			}
		})
	}
	if err := validateNodeExecutionRun(base, base); err != nil {
		t.Fatalf("validateNodeExecutionRun() matching owner error = %v", err)
	}
}

type childRunRecoveryFixture struct {
	ctx        context.Context
	runner     *GraphRunner
	store      *MemoryExecutionStore
	request    ChildRunRequest
	input      *state.State
	parentID   string
	childID    string
	requestKey string
}

func newChildRunRecoveryFixture(t *testing.T, finalized bool) childRunRecoveryFixture {
	t.Helper()
	return newChildRunFixture(t, finalized, RunStatusFailed, "persisted child failure")
}

func newChildRunFixture(t *testing.T, finalized bool, childStatus RunStatus, errorMessage string) childRunRecoveryFixture {
	t.Helper()

	fixedTime := time.Unix(400, 0)
	request := ChildRunRequest{
		ParentRunID:  "parent-run",
		ParentStepID: "parent-step",
		ParentTaskID: "parent-task",
		GraphRef:     "child-graph-ref",
		Namespace:    "root/child",
	}
	input := state.NewState()
	inputHash, err := childRunInputHash(input)
	if err != nil {
		t.Fatalf("childRunInputHash() error = %v", err)
	}
	requestKey := childRunRequestKey(request)
	childID := childRunIDForRequestKey(requestKey)
	reservation := PendingChildRun{
		RequestKey: requestKey, ChildRunID: childID,
		ParentRunID: request.ParentRunID, ParentStepID: request.ParentStepID, ParentTaskID: request.ParentTaskID,
		GraphRef: request.GraphRef, GraphID: "child-graph", GraphVersion: "v1",
		Namespace: request.Namespace, InputHash: inputHash, ReservedAt: fixedTime,
	}
	parent := RunRecord{
		RunID: request.ParentRunID, Status: RunStatusRunning,
		RootRunID: request.ParentRunID, RunPath: []string{request.ParentRunID}, Namespace: "root",
		PendingChildRuns: []PendingChildRun{reservation},
	}
	child := RunRecord{
		RunID: childID, ChildRequestKey: requestKey, ChildInputHash: inputHash,
		ParentRunID: request.ParentRunID, ParentStepID: request.ParentStepID, ParentTaskID: request.ParentTaskID,
		RootRunID: request.ParentRunID, RunPath: []string{request.ParentRunID, childID}, Namespace: request.Namespace,
		GraphID: "child-graph", GraphVersion: "v1", Status: childStatus, ErrorMessage: errorMessage,
		EntryNodeID: "start", CurrentNodeID: "start",
		StartedAt: fixedTime, UpdatedAt: fixedTime,
	}
	store := NewMemoryExecutionStore()
	if err := store.CreateRun(context.Background(), parent); err != nil {
		t.Fatalf("CreateRun(parent) error = %v", err)
	}
	if err := store.CreateRun(context.Background(), child); err != nil {
		t.Fatalf("CreateRun(child) error = %v", err)
	}
	if finalized {
		persistedParent, err := store.GetRun(context.Background(), parent.RunID)
		if err != nil {
			t.Fatalf("GetRun(parent) error = %v", err)
		}
		if _, err := finalizePendingChildRun(&persistedParent, requestKey, childID, fixedTime); err != nil {
			t.Fatalf("finalizePendingChildRun() error = %v", err)
		}
		if _, err := store.CompareAndSwapRun(context.Background(), persistedParent.Revision, persistedParent); err != nil {
			t.Fatalf("CompareAndSwapRun(parent) error = %v", err)
		}
	}
	ctx := WithRunnerMetadata(context.Background(), RunnerMetadata{
		RunID: request.ParentRunID, StepID: request.ParentStepID, TaskID: request.ParentTaskID,
	})
	return childRunRecoveryFixture{
		ctx: ctx, runner: &GraphRunner{
			executionStore: store, graphID: "child-graph", graphVersion: "v1", now: func() time.Time { return fixedTime },
		},
		store: store, request: request, input: input,
		parentID: request.ParentRunID, childID: childID, requestKey: requestKey,
	}
}

func assertChildRunRecovery(t *testing.T, fixture childRunRecoveryFixture) {
	t.Helper()

	result, err := fixture.runner.RunChild(fixture.ctx, fixture.request, fixture.input)
	if err == nil || !strings.Contains(err.Error(), "persisted child failure") {
		t.Fatalf("RunChild() error = %v", err)
	}
	if !result.Resumed || result.Run.RunID != fixture.childID {
		t.Fatalf("RunChild() result = %#v", result)
	}
	parent, err := fixture.store.GetRun(context.Background(), fixture.parentID)
	if err != nil {
		t.Fatalf("GetRun(parent) error = %v", err)
	}
	if len(parent.PendingChildRuns) != 0 {
		t.Fatalf("pending child reservations = %#v", parent.PendingChildRuns)
	}
	if len(parent.ChildRunIDs) != 1 || parent.ChildRunIDs[0] != fixture.childID {
		t.Fatalf("child run links = %#v", parent.ChildRunIDs)
	}
	runs, err := fixture.store.ListRuns(context.Background(), RunFilter{})
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("persisted run count = %d, want 2", len(runs))
	}
}

func newTestPendingChildRun(requestKey string, reservedAt time.Time) PendingChildRun {
	return PendingChildRun{
		RequestKey: requestKey, ChildRunID: childRunIDForRequestKey(requestKey),
		ParentRunID: "parent-run", ParentStepID: "parent-step", ParentTaskID: "parent-task",
		GraphRef: "child-graph-ref", GraphID: "child-graph", GraphVersion: "v1",
		Namespace: "root/child", InputHash: "input-hash", ReservedAt: reservedAt,
	}
}

type blockingChildRunGraph struct {
	invocations atomic.Int32
	entered     chan struct{}
	release     chan struct{}
}

type runCheckHookTransactionStore struct {
	TransactionStore
	once             sync.Once
	commitAttempts   atomic.Int32
	checkAttempts    atomic.Int32
	beforeFirstCheck func(context.Context) error
	alwaysConflict   bool
}

func (store *runCheckHookTransactionStore) Commit(ctx context.Context, commit Commit) (CommitResult, error) {
	store.commitAttempts.Add(1)
	if commit.Run != nil && commit.Run.Mode == RunWriteCheck {
		store.checkAttempts.Add(1)
		var hookErr error
		store.once.Do(func() {
			if store.beforeFirstCheck != nil {
				hookErr = store.beforeFirstCheck(ctx)
			}
		})
		if hookErr != nil {
			return CommitResult{}, hookErr
		}
	}
	if store.alwaysConflict {
		return CommitResult{}, ErrRunRevisionConflict
	}
	return store.TransactionStore.Commit(ctx, commit)
}

func (*blockingChildRunGraph) Validate() error { return nil }

func (*blockingChildRunGraph) EntryPointID() string { return "start" }

func (workflow *blockingChildRunGraph) CompileForRunner(RunnerExecution) (RunnerRunnable, error) {
	return blockingChildRunRunnable{workflow: workflow}, nil
}

func (*blockingChildRunGraph) ResolveNodeID(nodeID string) (string, error) { return nodeID, nil }

func (*blockingChildRunGraph) ResolveEdgeTarget(target string) (string, error) { return target, nil }

func (*blockingChildRunGraph) ResolveNextNodes(context.Context, string, *state.State) ([]string, error) {
	return nil, nil
}

func (*blockingChildRunGraph) ResolveNextNode(context.Context, string, *state.State) (string, error) {
	return EndNodeID, nil
}

func (*blockingChildRunGraph) ResolveNextTasks(context.Context, GraphTask, *state.State) ([]GraphTask, error) {
	return nil, nil
}

func (*blockingChildRunGraph) ResolveFailure(context.Context, GraphTask, string, error) ([]GraphTask, error) {
	return nil, nil
}

func (*blockingChildRunGraph) IsParallelBranchTarget(string) bool { return false }

func (*blockingChildRunGraph) NodeName(nodeID string) string { return nodeID }

func (*blockingChildRunGraph) AfterInterruptNodes([]Breakpoint) ([]string, error) { return nil, nil }

type blockingChildRunRunnable struct {
	workflow *blockingChildRunGraph
}

func newTestChildExecutionRunner(fixture childRunRecoveryFixture, workflow RunnerGraph, checkpointStore CheckpointStore, eventSink EventSink, transactionStore TransactionStore) *GraphRunner {
	return &GraphRunner{
		graph: workflow, executionStore: fixture.store, checkpointStore: checkpointStore,
		artifactStore: NewNoopArtifactStore(), codec: state.NewJSONStateCodec(""), eventSink: eventSink,
		transactionStore: transactionStore, graphID: "child-graph", graphVersion: "v1", now: time.Now,
		activeExecutions: map[string]*graphRunnerExecution{}, executionClaims: map[string]struct{}{},
	}
}

func (runnable blockingChildRunRunnable) InvokeWithConfig(_ context.Context, initialState *state.State, _ SchedulerConfig) (*state.State, error) {
	runnable.workflow.invocations.Add(1)
	select {
	case runnable.workflow.entered <- struct{}{}:
	default:
	}
	<-runnable.workflow.release
	return initialState.Clone(), nil
}
