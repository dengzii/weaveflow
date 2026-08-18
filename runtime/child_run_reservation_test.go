package runtime

import (
	"context"
	"errors"
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
	resumeChildRunFixtureAtStep(&fixture, "resumed-parent-step")
	assertChildRunRecovery(t, fixture)
}

func TestChildRunFinalizedRetryDoesNotCreateSecondRun(t *testing.T) {
	t.Parallel()

	fixture := newChildRunRecoveryFixture(t, true)
	resumeChildRunFixtureAtStep(&fixture, "resumed-parent-step")
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

func TestChildRunPendingReservationCreatesChildWithOriginalStepAcrossResumeAttempts(t *testing.T) {
	t.Parallel()

	fixedTime := time.Unix(320, 0)
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
	reservation := PendingChildRun{
		RequestKey: requestKey, ChildRunID: childRunIDForRequestKey(requestKey),
		ParentRunID: request.ParentRunID, ParentStepID: request.ParentStepID, ParentTaskID: request.ParentTaskID,
		GraphRef: request.GraphRef, GraphID: "child-graph", GraphVersion: "v1",
		Namespace: request.Namespace, InputHash: inputHash, ReservedAt: fixedTime,
	}
	executionStore := NewMemoryExecutionStore()
	parent := RunRecord{
		RunID: request.ParentRunID, Status: RunStatusRunning,
		RootRunID: request.ParentRunID, RunPath: []string{request.ParentRunID}, Namespace: "root",
		PendingChildRuns: []PendingChildRun{reservation},
	}
	if err := executionStore.CreateRun(context.Background(), parent); err != nil {
		t.Fatalf("CreateRun(parent) error = %v", err)
	}
	checkpointStore := NewMemoryCheckpointStore()
	eventSink := NewMemoryEventSink()
	transactionStore, err := resolveRuntimeTransactionStore(executionStore, checkpointStore, eventSink)
	if err != nil {
		t.Fatalf("resolveRuntimeTransactionStore() error = %v", err)
	}
	release := make(chan struct{})
	close(release)
	fixture := childRunRecoveryFixture{store: executionStore}
	runner := newTestChildExecutionRunner(fixture, &blockingChildRunGraph{
		entered: make(chan struct{}, 1),
		release: release,
	}, checkpointStore, eventSink, transactionStore)

	request.ParentStepID = "resumed-parent-step"
	ctx := WithRunnerMetadata(context.Background(), RunnerMetadata{
		RunID: request.ParentRunID, StepID: request.ParentStepID, TaskID: request.ParentTaskID,
	})
	result, err := runner.RunChild(ctx, request, input)
	if err != nil {
		t.Fatalf("RunChild() error = %v", err)
	}
	if result.Resumed {
		t.Fatal("RunChild() treated a pending-only reservation as an existing child run")
	}
	if result.Run.RunID != reservation.ChildRunID {
		t.Fatalf("RunChild() run ID = %q, want %q", result.Run.RunID, reservation.ChildRunID)
	}
	if result.Run.ParentStepID != reservation.ParentStepID {
		t.Fatalf("RunChild() parent step ID = %q, want original %q", result.Run.ParentStepID, reservation.ParentStepID)
	}
	persistedChild, err := executionStore.GetRun(context.Background(), reservation.ChildRunID)
	if err != nil {
		t.Fatalf("GetRun(child) error = %v", err)
	}
	if persistedChild.ParentStepID != reservation.ParentStepID {
		t.Fatalf("persisted child parent step ID = %q, want original %q", persistedChild.ParentStepID, reservation.ParentStepID)
	}
	persistedParent, err := executionStore.GetRun(context.Background(), request.ParentRunID)
	if err != nil {
		t.Fatalf("GetRun(parent) error = %v", err)
	}
	if len(persistedParent.PendingChildRuns) != 0 {
		t.Fatalf("pending child reservations = %#v", persistedParent.PendingChildRuns)
	}
	if len(persistedParent.ChildRunIDs) != 1 || persistedParent.ChildRunIDs[0] != reservation.ChildRunID {
		t.Fatalf("child run links = %#v", persistedParent.ChildRunIDs)
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
		{name: "execution lease", change: func(run *RunRecord) {
			now := time.Unix(1, 0)
			run.ExecutionLease = &ExecutionLease{
				OwnerID: "owner", Token: "not/portable", Epoch: 1, Status: ExecutionLeaseActive,
				AcquiredAt: now, HeartbeatAt: now, ExpiresAt: now.Add(time.Minute),
			}
		}, want: "portable record ID"},
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

type childRunRecoveryFixture struct {
	ctx          context.Context
	runner       *GraphRunner
	store        *MemoryExecutionStore
	request      ChildRunRequest
	input        *state.State
	parentID     string
	parentStepID string
	childID      string
	requestKey   string
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
		parentID: request.ParentRunID, parentStepID: request.ParentStepID, childID: childID, requestKey: requestKey,
	}
}

func resumeChildRunFixtureAtStep(fixture *childRunRecoveryFixture, parentStepID string) {
	fixture.request.ParentStepID = parentStepID
	fixture.ctx = WithRunnerMetadata(context.Background(), RunnerMetadata{
		RunID: fixture.request.ParentRunID, StepID: parentStepID, TaskID: fixture.request.ParentTaskID,
	})
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
	if result.Run.ParentStepID != fixture.parentStepID {
		t.Fatalf("RunChild() parent step ID = %q, want original %q", result.Run.ParentStepID, fixture.parentStepID)
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
	persistedChild, err := fixture.store.GetRun(context.Background(), fixture.childID)
	if err != nil {
		t.Fatalf("GetRun(child) error = %v", err)
	}
	if persistedChild.ParentStepID != fixture.parentStepID {
		t.Fatalf("persisted child parent step ID = %q, want original %q", persistedChild.ParentStepID, fixture.parentStepID)
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
		activeExecutions: map[string]*graphRunnerExecution{},
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
