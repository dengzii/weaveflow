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

func TestPauseTransitionsStopAfterRevisionRetryLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call func(*GraphRunner, RunRecord, StepRecord) error
	}{
		{
			name: "node checkpoint",
			call: func(runner *GraphRunner, run RunRecord, step StepRecord) error {
				_, _, err := runner.pauseRun(context.Background(), run, state.NewState(), step, step.CheckpointBeforeID, nil, "")
				return err
			},
		},
		{
			name: "wave checkpoint",
			call: func(runner *GraphRunner, run RunRecord, _ StepRecord) error {
				_, _, err := runner.pauseRunAtCheckpoint(context.Background(), run, state.NewState(), "checkpoint", nil, "")
				return err
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			now := time.Unix(600, 0)
			run := RunRecord{RunID: "pause-limit-" + strings.ReplaceAll(testCase.name, " ", "-"), GraphID: "graph", Status: RunStatusRunning, StartedAt: now, UpdatedAt: now}
			step := StepRecord{
				StepID: "step", RunID: run.RunID, TaskID: "task", NodeID: "node", Status: StepStatusRunning,
				CheckpointBeforeID: "checkpoint", StartedAt: now, UpdatedAt: now,
			}
			store := NewMemoryRuntimeStore()
			if err := store.CreateRun(context.Background(), run); err != nil {
				t.Fatalf("CreateRun() error = %v", err)
			}
			if err := store.AppendStep(context.Background(), step); err != nil {
				t.Fatalf("AppendStep() error = %v", err)
			}
			hookStore := &pauseHookTransactionStore{TransactionStore: store, alwaysConflict: true}
			runner := newPauseTestRunner(store, hookStore, now)

			err := testCase.call(runner, run, step)
			if !errors.Is(err, ErrRunRevisionConflict) || !strings.Contains(err.Error(), "exceeded 8") {
				t.Fatalf("pause error = %v, want bounded revision conflict", err)
			}
			if attempts := hookStore.updateAttempts.Load(); attempts != 8 {
				t.Fatalf("pause update attempts = %d, want 8", attempts)
			}
			persisted, err := store.GetRun(context.Background(), run.RunID)
			if err != nil {
				t.Fatalf("GetRun() error = %v", err)
			}
			if persisted.Status != RunStatusRunning || persisted.PauseRequested || persisted.CancelRequested {
				t.Fatalf("persisted run = %#v", persisted)
			}
		})
	}
}

func TestPauseRunRetriesConflictAndLetsCancelWin(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Unix(601, 0)
	run := RunRecord{
		RunID: "pause-cancel-node", GraphID: "graph", Status: RunStatusRunning,
		PauseRequested: true, StartedAt: now, UpdatedAt: now,
	}
	step := StepRecord{
		StepID: "step", RunID: run.RunID, TaskID: "task", NodeID: "node", Attempt: 2, Status: StepStatusRunning,
		CheckpointBeforeID: "checkpoint", StartedAt: now, UpdatedAt: now,
	}
	store := NewMemoryRuntimeStore()
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	if err := store.AppendStep(ctx, step); err != nil {
		t.Fatalf("AppendStep() error = %v", err)
	}
	hookStore := &pauseHookTransactionStore{TransactionStore: store}
	hookStore.beforeFirstUpdate = func(ctx context.Context) error {
		persisted, err := store.GetRun(ctx, run.RunID)
		if err != nil {
			return err
		}
		persisted.PauseRequested = false
		persisted.CancelRequested = true
		_, err = store.CompareAndSwapRun(ctx, persisted.Revision, persisted)
		return err
	}
	runner := newPauseTestRunner(store, hookStore, now)

	canceled, _, err := runner.pauseRun(ctx, run, state.NewState(), step, step.CheckpointBeforeID, nil, "")
	if err != nil {
		t.Fatalf("pauseRun() error = %v", err)
	}
	if canceled.Status != RunStatusCanceled || canceled.PauseRequested || canceled.CancelRequested || canceled.FinishedAt == nil {
		t.Fatalf("canceled run = %#v", canceled)
	}
	if attempts := hookStore.updateAttempts.Load(); attempts != 2 {
		t.Fatalf("pause update attempts = %d, want 2", attempts)
	}
	persistedStep, err := store.GetStep(ctx, step.StepID)
	if err != nil {
		t.Fatalf("GetStep() error = %v", err)
	}
	if persistedStep.Status != StepStatusCanceled || persistedStep.ErrorCode != "run_canceled" || persistedStep.FinishedAt == nil {
		t.Fatalf("canceled step = %#v", persistedStep)
	}
	events, err := store.ListEvents(run.RunID)
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	assertCancelWonPauseRace(t, events, true)
}

func TestRunLifecycleTransitionsStopAfterRevisionRetryLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status RunStatus
		call   func(*GraphRunner, RunRecord, *state.State) error
	}{
		{name: "complete", status: RunStatusRunning, call: func(runner *GraphRunner, run RunRecord, currentState *state.State) error {
			_, _, err := runner.completeRun(context.Background(), run, currentState, nil)
			return err
		}},
		{name: "cancel", status: RunStatusRunning, call: func(runner *GraphRunner, run RunRecord, currentState *state.State) error {
			_, _, err := runner.cancelRunWithTransition(context.Background(), run, currentState, runnerStepTransition{})
			return err
		}},
		{name: "fail", status: RunStatusRunning, call: func(runner *GraphRunner, run RunRecord, currentState *state.State) error {
			_, err := runner.persistRunFailureWithTransition(context.Background(), run, currentState, "node_failed", "node failed", runnerStepTransition{})
			return err
		}},
		{name: "resume", status: RunStatusPaused, call: func(runner *GraphRunner, run RunRecord, currentState *state.State) error {
			checkpoint := RestoredCheckpoint{
				Record:   CheckpointRecord{CheckpointID: "checkpoint", RunID: run.RunID, NodeID: waveCheckpointNodeID, Stage: CheckpointAfterWave},
				Business: currentState,
			}
			_, _, err := runner.resumeExistingRun(context.Background(), run, checkpoint, nil)
			return err
		}},
		{name: "abort", status: RunStatusRunning, call: func(runner *GraphRunner, run RunRecord, _ *state.State) error {
			return runner.abortStartedRun(context.Background(), run, "startup_failed", errors.New("startup failed"))
		}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			now := time.Unix(610, 0)
			run := RunRecord{
				RunID: "lifecycle-limit-" + testCase.name, GraphID: "graph", Status: testCase.status,
				EntryNodeID: "start", CurrentNodeID: "start", StartedAt: now, UpdatedAt: now,
			}
			store := NewMemoryRuntimeStore()
			if err := store.CreateRun(context.Background(), run); err != nil {
				t.Fatalf("CreateRun() error = %v", err)
			}
			currentState := state.NewState()
			if err := StoreGraphSchedule(currentState, GraphSchedule{NextTasks: []GraphTask{NewStaticGraphTask("start", 0)}}); err != nil {
				t.Fatalf("StoreGraphSchedule() error = %v", err)
			}
			hookStore := &pauseHookTransactionStore{TransactionStore: store, alwaysConflict: true}
			runner := newPauseTestRunner(store, hookStore, now)

			err := testCase.call(runner, run, currentState)
			if !errors.Is(err, ErrRunRevisionConflict) || !strings.Contains(err.Error(), "exceeded 8") {
				t.Fatalf("lifecycle error = %v, want bounded revision conflict", err)
			}
			if attempts := hookStore.updateAttempts.Load(); attempts != 8 {
				t.Fatalf("lifecycle update attempts = %d, want 8", attempts)
			}
			persisted, err := store.GetRun(context.Background(), run.RunID)
			if err != nil {
				t.Fatalf("GetRun() error = %v", err)
			}
			if persisted.Status != testCase.status {
				t.Fatalf("persisted status = %q, want %q", persisted.Status, testCase.status)
			}
		})
	}
}

func TestCompleteRunRetriesConflictAndLetsCancelWin(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Unix(611, 0)
	run := RunRecord{RunID: "complete-cancel", GraphID: "graph", Status: RunStatusRunning, StartedAt: now, UpdatedAt: now}
	store := NewMemoryRuntimeStore()
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	hookStore := &pauseHookTransactionStore{TransactionStore: store}
	hookStore.beforeFirstUpdate = func(ctx context.Context) error {
		persisted, err := store.GetRun(ctx, run.RunID)
		if err != nil {
			return err
		}
		persisted.CancelRequested = true
		_, err = store.CompareAndSwapRun(ctx, persisted.Revision, persisted)
		return err
	}
	runner := newPauseTestRunner(store, hookStore, now)

	completed, _, err := runner.completeRun(ctx, run, state.NewState(), nil)
	if err != nil {
		t.Fatalf("completeRun() error = %v", err)
	}
	if completed.Status != RunStatusCanceled || completed.PauseRequested || completed.CancelRequested {
		t.Fatalf("completed run = %#v", completed)
	}
	if attempts := hookStore.updateAttempts.Load(); attempts != 2 {
		t.Fatalf("complete update attempts = %d, want 2", attempts)
	}
	events, err := store.ListEvents(run.RunID)
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	assertCancelWonPauseRace(t, events, false)
	for _, event := range events {
		if event.Type == EventRunFinished || event.Type == EventCheckpointCreated {
			t.Fatalf("completion event persisted after cancel won: %#v", event)
		}
	}
}

func TestAbortStartedRunRetriesConflictAndLetsCancelWin(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Unix(611, 0)
	run := RunRecord{RunID: "abort-cancel", GraphID: "graph", Status: RunStatusRunning, StartedAt: now, UpdatedAt: now}
	store := NewMemoryRuntimeStore()
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	hookStore := &pauseHookTransactionStore{TransactionStore: store}
	hookStore.beforeFirstUpdate = func(ctx context.Context) error {
		persisted, err := store.GetRun(ctx, run.RunID)
		if err != nil {
			return err
		}
		persisted.CancelRequested = true
		_, err = store.CompareAndSwapRun(ctx, persisted.Revision, persisted)
		return err
	}
	runner := newPauseTestRunner(store, hookStore, now)
	cause := errors.New("startup failed")

	err := runner.abortStartedRun(ctx, run, "startup_failed", cause)
	if !errors.Is(err, cause) {
		t.Fatalf("abortStartedRun() error = %v, want startup cause", err)
	}
	if attempts := hookStore.updateAttempts.Load(); attempts != 2 {
		t.Fatalf("abort update attempts = %d, want 2", attempts)
	}
	persisted, err := store.GetRun(ctx, run.RunID)
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if persisted.Status != RunStatusCanceled || persisted.CancelRequested || persisted.FinishedAt == nil {
		t.Fatalf("aborted run = %#v, want canceled", persisted)
	}
	events, err := store.ListEvents(run.RunID)
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(events) != 1 || events[0].Type != EventRunCanceled {
		t.Fatalf("abort events = %#v, want only run canceled", events)
	}
}

func TestFailRunRetriesConflictAndLetsPauseWin(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Unix(612, 0)
	run := RunRecord{
		RunID: "fail-pause", GraphID: "graph", Status: RunStatusRunning, CurrentNodeID: "node",
		LastCheckpointID: "checkpoint", StartedAt: now, UpdatedAt: now,
	}
	step := StepRecord{
		StepID: "step", RunID: run.RunID, TaskID: "task", NodeID: "node", Attempt: 1, Status: StepStatusRunning,
		CheckpointBeforeID: run.LastCheckpointID, StartedAt: now, UpdatedAt: now,
	}
	store := NewMemoryRuntimeStore()
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	if err := store.AppendStep(ctx, step); err != nil {
		t.Fatalf("AppendStep() error = %v", err)
	}
	checkpoint := CheckpointRecord{
		CheckpointID: run.LastCheckpointID, RunID: run.RunID, StepID: step.StepID, TaskID: step.TaskID,
		NodeID: step.NodeID, Stage: CheckpointBeforeNode, CreatedAt: now,
	}
	if err := store.Save(ctx, checkpoint, []byte(`{}`)); err != nil {
		t.Fatalf("Save(checkpoint) error = %v", err)
	}
	hookStore := &pauseHookTransactionStore{TransactionStore: store}
	hookStore.beforeFirstUpdate = func(ctx context.Context) error {
		persisted, err := store.GetRun(ctx, run.RunID)
		if err != nil {
			return err
		}
		persisted.PauseRequested = true
		_, err = store.CompareAndSwapRun(ctx, persisted.Revision, persisted)
		return err
	}
	runner := newPauseTestRunner(store, hookStore, now)
	failedStep := step
	failedStep.Status = StepStatusFailed
	failedStep.ErrorCode = "node_failed"
	failedStep.ErrorMessage = "node failed"
	failedStep.FinishedAt = &now
	transition := runnerStepTransition{
		writes: []StepWrite{{Mode: StepWriteUpdate, Step: failedStep}},
		events: []Event{{ID: "node-failed", RunID: run.RunID, StepID: step.StepID, TaskID: step.TaskID, NodeID: step.NodeID, Type: EventNodeFailed}},
		steps:  []StepRecord{failedStep},
	}

	paused, err := runner.persistRunFailureWithTransition(ctx, run, state.NewState(), "node_failed", "node failed", transition)
	if err != nil {
		t.Fatalf("persistRunFailureWithTransition() error = %v", err)
	}
	if paused.Status != RunStatusPaused || paused.PauseRequested || paused.CancelRequested || paused.FinishedAt != nil {
		t.Fatalf("paused run = %#v", paused)
	}
	persistedStep, err := store.GetStep(ctx, step.StepID)
	if err != nil {
		t.Fatalf("GetStep() error = %v", err)
	}
	if persistedStep.Status != StepStatusPaused || persistedStep.ErrorCode != "" || persistedStep.FinishedAt != nil {
		t.Fatalf("paused step = %#v", persistedStep)
	}
	events, err := store.ListEvents(run.RunID)
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	for _, event := range events {
		if event.Type == EventRunFailed || event.Type == EventNodeFailed {
			t.Fatalf("failure event persisted after pause won: %#v", event)
		}
	}
	if len(events) != 1 || events[0].Type != EventRunPaused {
		t.Fatalf("pause events = %#v", events)
	}
}

func TestResumeRetriesConflictWithoutClearingPause(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Unix(613, 0)
	run := RunRecord{
		RunID: "resume-pause", GraphID: "graph", Status: RunStatusPaused, EntryNodeID: "start",
		LastCheckpointID: "checkpoint", StartedAt: now, UpdatedAt: now,
	}
	store := NewMemoryRuntimeStore()
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	checkpointRecord := CheckpointRecord{
		CheckpointID: run.LastCheckpointID, RunID: run.RunID, NodeID: waveCheckpointNodeID,
		Stage: CheckpointAfterWave, CreatedAt: now,
	}
	if err := store.Save(ctx, checkpointRecord, []byte(`{}`)); err != nil {
		t.Fatalf("Save(checkpoint) error = %v", err)
	}
	currentState := state.NewState()
	if err := StoreGraphSchedule(currentState, GraphSchedule{}); err != nil {
		t.Fatalf("StoreGraphSchedule() error = %v", err)
	}
	checkpoint := RestoredCheckpoint{Record: checkpointRecord, Business: currentState}
	hookStore := &pauseHookTransactionStore{TransactionStore: store}
	hookStore.beforeFirstUpdate = func(ctx context.Context) error {
		persisted, err := store.GetRun(ctx, run.RunID)
		if err != nil {
			return err
		}
		persisted.PauseRequested = true
		_, err = store.CompareAndSwapRun(ctx, persisted.Revision, persisted)
		return err
	}
	runner := newPauseTestRunner(store, hookStore, now)

	paused, _, err := runner.resumeExistingRun(ctx, run, checkpoint, nil)
	if err != nil {
		t.Fatalf("resumeExistingRun() error = %v", err)
	}
	if paused.Status != RunStatusPaused || paused.PauseRequested || paused.CancelRequested || paused.LastCheckpointID != run.LastCheckpointID {
		t.Fatalf("paused resumed run = %#v", paused)
	}
	if attempts := hookStore.updateAttempts.Load(); attempts != 3 {
		t.Fatalf("resume update attempts = %d, want 3", attempts)
	}
	events, err := store.ListEvents(run.RunID)
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(events) != 2 || events[0].Type != EventRunResumed || events[1].Type != EventRunPaused {
		t.Fatalf("resume pause events = %#v", events)
	}
}

func TestResumeDoesNotContinueConcurrentTerminalRun(t *testing.T) {
	t.Parallel()

	for _, terminalStatus := range []RunStatus{RunStatusCanceled, RunStatusCompleted, RunStatusFailed} {
		terminalStatus := terminalStatus
		t.Run(string(terminalStatus), func(t *testing.T) {
			ctx := context.Background()
			now := time.Unix(614, 0)
			run := RunRecord{
				RunID: "resume-" + string(terminalStatus), GraphID: "graph", Status: RunStatusPaused, EntryNodeID: "start",
				LastCheckpointID: "checkpoint", StartedAt: now, UpdatedAt: now,
			}
			store := NewMemoryRuntimeStore()
			if err := store.CreateRun(ctx, run); err != nil {
				t.Fatalf("CreateRun() error = %v", err)
			}
			currentState := state.NewState()
			if err := StoreGraphSchedule(currentState, GraphSchedule{NextTasks: []GraphTask{NewStaticGraphTask("start", 0)}}); err != nil {
				t.Fatalf("StoreGraphSchedule() error = %v", err)
			}
			checkpoint := RestoredCheckpoint{
				Record:   CheckpointRecord{CheckpointID: run.LastCheckpointID, RunID: run.RunID, NodeID: waveCheckpointNodeID, Stage: CheckpointAfterWave},
				Business: currentState,
			}
			hookStore := &pauseHookTransactionStore{TransactionStore: store}
			hookStore.beforeFirstUpdate = func(ctx context.Context) error {
				persisted, err := store.GetRun(ctx, run.RunID)
				if err != nil {
					return err
				}
				persisted.Status = terminalStatus
				persisted.PauseRequested = false
				persisted.CancelRequested = false
				persisted.FinishedAt = &now
				_, err = store.CompareAndSwapRun(ctx, persisted.Revision, persisted)
				return err
			}
			runner := newPauseTestRunner(store, hookStore, now)

			terminalRun, _, err := runner.resumeExistingRun(ctx, run, checkpoint, nil)
			if err != nil {
				t.Fatalf("resumeExistingRun() error = %v", err)
			}
			if terminalRun.Status != terminalStatus || terminalRun.FinishedAt == nil {
				t.Fatalf("terminal run = %#v", terminalRun)
			}
			if attempts := hookStore.updateAttempts.Load(); attempts != 1 {
				t.Fatalf("resume update attempts = %d, want 1", attempts)
			}
			events, err := store.ListEvents(run.RunID)
			if err != nil {
				t.Fatalf("ListEvents() error = %v", err)
			}
			if len(events) != 0 {
				t.Fatalf("events after concurrent terminal transition = %#v", events)
			}
		})
	}
}

func assertCancelWonPauseRace(t *testing.T, events []Event, wantNodeCanceled bool) {
	t.Helper()

	seenNodeCanceled := false
	seenRunCanceled := false
	for _, event := range events {
		switch event.Type {
		case EventNodeCanceled:
			seenNodeCanceled = true
		case EventRunCanceled:
			seenRunCanceled = true
		case EventRunPaused, EventBreakpointHit:
			t.Fatalf("pause event persisted after cancel won: %#v", event)
		}
	}
	if seenNodeCanceled != wantNodeCanceled || !seenRunCanceled {
		t.Fatalf("cancel events = %#v", events)
	}
}

func newPauseTestRunner(store *MemoryRuntimeStore, transactionStore TransactionStore, now time.Time) *GraphRunner {
	return &GraphRunner{
		executionStore:   store,
		checkpointStore:  store,
		transactionStore: transactionStore,
		eventSink:        NoopEventSink{},
		codec:            state.NewJSONStateCodec(""),
		now:              func() time.Time { return now },
	}
}

func createPauseTestChildRun(t *testing.T, store *MemoryRuntimeStore, run RunRecord, now time.Time) {
	t.Helper()

	reservation := PendingChildRun{
		RequestKey: run.ChildRequestKey, ChildRunID: run.RunID,
		ParentRunID: run.ParentRunID, ParentStepID: run.ParentStepID, ParentTaskID: run.ParentTaskID,
		GraphRef: "child-ref", GraphID: run.GraphID, GraphVersion: run.GraphVersion,
		Namespace: run.Namespace, InputHash: run.ChildInputHash, ReservedAt: now,
	}
	parent := RunRecord{
		RunID: run.ParentRunID, RootRunID: run.ParentRunID, RunPath: []string{run.ParentRunID}, Status: RunStatusRunning,
		PendingChildRuns: []PendingChildRun{reservation}, StartedAt: now, UpdatedAt: now,
	}
	if err := store.CreateRun(context.Background(), parent); err != nil {
		t.Fatalf("CreateRun(parent) error = %v", err)
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("CreateRun(child) error = %v", err)
	}
}

type pauseHookTransactionStore struct {
	TransactionStore
	once              sync.Once
	updateAttempts    atomic.Int32
	beforeFirstUpdate func(context.Context) error
	alwaysConflict    bool
}

func (store *pauseHookTransactionStore) Commit(ctx context.Context, commit Commit) (CommitResult, error) {
	if commit.Run == nil || commit.Run.Mode != RunWriteUpdate {
		return store.TransactionStore.Commit(ctx, commit)
	}
	store.updateAttempts.Add(1)
	var hookErr error
	store.once.Do(func() {
		if store.beforeFirstUpdate != nil {
			hookErr = store.beforeFirstUpdate(ctx)
		}
	})
	if hookErr != nil {
		return CommitResult{}, hookErr
	}
	if store.alwaysConflict {
		return CommitResult{}, ErrRunRevisionConflict
	}
	return store.TransactionStore.Commit(ctx, commit)
}
