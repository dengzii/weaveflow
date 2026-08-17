package runtime

import (
	"context"
	"errors"
	"testing"
)

type commitReader interface {
	TransactionStore
	GetRun(context.Context, string) (RunRecord, error)
	ListEvents(string) ([]Event, error)
}

type checkpointEventCommitReader interface {
	TransactionStore
	Load(context.Context, string) (CheckpointRecord, []byte, error)
	ListEvents(string) ([]Event, error)
}

type deletionAwareCommitStore interface {
	commitReader
	RunDeletionExecutionStore
	RunDeletionFencer
	CheckpointStore
	CreateRun(context.Context, RunRecord) error
	GetStep(context.Context, string) (StepRecord, error)
}

type recordingRuntimeTransactionStore struct {
	commits []Commit
}

func (store *recordingRuntimeTransactionStore) Commit(_ context.Context, commit Commit) (CommitResult, error) {
	store.commits = append(store.commits, commit)
	if commit.Run == nil || commit.Run.Mode == RunWriteCheck {
		return CommitResult{}, nil
	}
	run := commit.Run.Run
	run.Revision++
	return CommitResult{Run: &run}, nil
}

func TestMemoryRuntimeStoreCommitRejectsStaleRunRevision(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	runtimeStore := NewMemoryRuntimeStore()
	original := RunRecord{RunID: "run-memory-cas", Status: RunStatusPending}
	if err := runtimeStore.CreateRun(ctx, original); err != nil {
		t.Fatalf("CreateRun(): %v", err)
	}

	firstUpdate := original
	firstUpdate.Status = RunStatusRunning
	firstResult, err := runtimeStore.Commit(ctx, Commit{Run: &RunWrite{Mode: RunWriteUpdate, Run: firstUpdate}})
	if err != nil {
		t.Fatalf("first Commit(): %v", err)
	}
	if firstResult.Run == nil || firstResult.Run.Revision != 1 {
		t.Fatalf("first commit result = %#v", firstResult.Run)
	}

	staleUpdate := original
	staleUpdate.Status = RunStatusCompleted
	if _, err := runtimeStore.Commit(ctx, Commit{Run: &RunWrite{Mode: RunWriteUpdate, Run: staleUpdate}}); !errors.Is(err, ErrRunRevisionConflict) {
		t.Fatalf("stale Commit() error = %v, want revision conflict", err)
	}
	persisted, err := runtimeStore.GetRun(ctx, original.RunID)
	if err != nil {
		t.Fatalf("GetRun(): %v", err)
	}
	if persisted.Revision != 1 || persisted.Status != RunStatusRunning {
		t.Fatalf("persisted run = %#v", persisted)
	}
}

func TestRuntimeStoreCommitChecksRunRevisionWithoutWritingRun(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		new  func(*testing.T) commitReader
	}{
		{name: "memory", new: func(*testing.T) commitReader { return NewMemoryRuntimeStore() }},
	}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			store := testCase.new(t)
			original := RunRecord{RunID: "run-check-revision", Status: RunStatusPending}
			if _, err := store.Commit(ctx, Commit{Run: &RunWrite{Mode: RunWriteCreate, Run: original}}); err != nil {
				t.Fatalf("Commit(create): %v", err)
			}

			checkedEvent := Event{ID: "event-checked-revision", RunID: original.RunID, Type: EventRunStarted}
			result, err := store.Commit(ctx, Commit{
				Run:    &RunWrite{Mode: RunWriteCheck, Run: original},
				Events: []Event{checkedEvent},
			})
			if err != nil {
				t.Fatalf("Commit(check): %v", err)
			}
			if result.Run != nil {
				t.Fatalf("check result run = %#v, want nil", result.Run)
			}
			persisted, err := store.GetRun(ctx, original.RunID)
			if err != nil {
				t.Fatalf("GetRun(after check): %v", err)
			}
			if persisted.Revision != original.Revision || persisted.Status != original.Status {
				t.Fatalf("run changed by check = %#v", persisted)
			}

			updated := persisted
			updated.Status = RunStatusRunning
			if _, err := store.Commit(ctx, Commit{Run: &RunWrite{Mode: RunWriteUpdate, Run: updated}}); err != nil {
				t.Fatalf("Commit(update): %v", err)
			}
			staleEvent := Event{ID: "event-stale-check", RunID: original.RunID, Type: EventRunFinished}
			if _, err := store.Commit(ctx, Commit{
				Run:    &RunWrite{Mode: RunWriteCheck, Run: original},
				Events: []Event{staleEvent},
			}); !errors.Is(err, ErrRunRevisionConflict) {
				t.Fatalf("Commit(stale check) error = %v, want revision conflict", err)
			}
			events, err := store.ListEvents(original.RunID)
			if err != nil {
				t.Fatalf("ListEvents(): %v", err)
			}
			if len(events) != 1 || events[0].ID != checkedEvent.ID {
				t.Fatalf("events after stale check = %#v", events)
			}
			persisted, err = store.GetRun(ctx, original.RunID)
			if err != nil {
				t.Fatalf("GetRun(after stale check): %v", err)
			}
			if persisted.Revision != 1 || persisted.Status != RunStatusRunning {
				t.Fatalf("run after stale check = %#v", persisted)
			}
		})
	}
}

func TestRuntimeStoreCommitCreatesRunWithEventsAtomically(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		new  func(*testing.T) commitReader
	}{
		{name: "memory", new: func(*testing.T) commitReader { return NewMemoryRuntimeStore() }},
	}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			store := testCase.new(t)
			run := RunRecord{RunID: "run-created-atomically", Status: RunStatusRunning}
			events := []Event{
				{ID: "event-run-created", RunID: run.RunID, Type: EventRunCreated},
				{ID: "event-run-started", RunID: run.RunID, Type: EventRunStarted},
			}
			result, err := store.Commit(context.Background(), Commit{
				Run:    &RunWrite{Mode: RunWriteCreate, Run: run},
				Events: events,
			})
			if err != nil {
				t.Fatalf("Commit(create): %v", err)
			}
			if result.Run == nil || result.Run.Revision != 0 || result.Run.Status != RunStatusRunning {
				t.Fatalf("create result = %#v", result.Run)
			}
			persisted, err := store.GetRun(context.Background(), run.RunID)
			if err != nil {
				t.Fatalf("GetRun(): %v", err)
			}
			if persisted.Status != run.Status || persisted.Revision != 0 {
				t.Fatalf("persisted run = %#v", persisted)
			}
			persistedEvents, err := store.ListEvents(run.RunID)
			if err != nil {
				t.Fatalf("ListEvents(): %v", err)
			}
			if len(persistedEvents) != 2 || persistedEvents[0].Type != EventRunCreated || persistedEvents[1].Type != EventRunStarted {
				t.Fatalf("persisted events = %#v", persistedEvents)
			}

			duplicate := Commit{
				Run:    &RunWrite{Mode: RunWriteCreate, Run: run},
				Events: []Event{{ID: "event-duplicate", RunID: run.RunID, Type: EventRunFailed}},
			}
			if _, err := store.Commit(context.Background(), duplicate); err == nil {
				t.Fatal("duplicate Commit(create) error = nil")
			}
			persistedEvents, err = store.ListEvents(run.RunID)
			if err != nil {
				t.Fatalf("ListEvents() after duplicate: %v", err)
			}
			if len(persistedEvents) != 2 {
				t.Fatalf("duplicate create appended events: %#v", persistedEvents)
			}
		})
	}
}

func TestGraphRunnerCommitsTerminalRunAndStepTransitionAtomically(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name       string
		stepStatus StepStatus
		nodeEvent  EventType
		runStatus  RunStatus
		runEvent   EventType
		commit     func(*GraphRunner, RunRecord, runnerStepTransition) error
	}{
		{
			name:       "failed",
			stepStatus: StepStatusFailed,
			nodeEvent:  EventNodeFailed,
			runStatus:  RunStatusFailed,
			runEvent:   EventRunFailed,
			commit: func(runner *GraphRunner, run RunRecord, transition runnerStepTransition) error {
				_, err := runner.persistRunFailureWithTransition(context.Background(), run, nil, "node_failed", "node failed", transition)
				return err
			},
		},
		{
			name:       "canceled",
			stepStatus: StepStatusCanceled,
			nodeEvent:  EventNodeCanceled,
			runStatus:  RunStatusCanceled,
			runEvent:   EventRunCanceled,
			commit: func(runner *GraphRunner, run RunRecord, transition runnerStepTransition) error {
				_, _, err := runner.cancelRunWithTransition(context.Background(), run, nil, transition)
				return err
			},
		},
	}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			store := &recordingRuntimeTransactionStore{}
			run := RunRecord{RunID: "terminal-run", Revision: 3, Status: RunStatusRunning, CurrentNodeID: "work"}
			step := StepRecord{RunID: run.RunID, StepID: "terminal-step", NodeID: "work", Status: testCase.stepStatus}
			executionStore := NewMemoryExecutionStore()
			if err := executionStore.CreateRun(context.Background(), run); err != nil {
				t.Fatalf("CreateRun() error = %v", err)
			}
			if err := executionStore.AppendStep(context.Background(), step); err != nil {
				t.Fatalf("AppendStep() error = %v", err)
			}
			runner := &GraphRunner{executionStore: executionStore, transactionStore: store}
			transition := runnerStepTransition{
				writes: []StepWrite{{Mode: StepWriteUpdate, Step: step}},
				events: []Event{{ID: "terminal-node-event", RunID: run.RunID, StepID: step.StepID, NodeID: step.NodeID, Type: testCase.nodeEvent}},
				steps:  []StepRecord{step},
			}
			if err := testCase.commit(runner, run, transition); err != nil {
				t.Fatalf("terminal commit: %v", err)
			}
			if len(store.commits) != 1 {
				t.Fatalf("commit count = %d, want 1", len(store.commits))
			}
			commit := store.commits[0]
			if commit.Run == nil || commit.Run.Mode != RunWriteUpdate || commit.Run.Run.Status != testCase.runStatus {
				t.Fatalf("run write = %#v, want status %q", commit.Run, testCase.runStatus)
			}
			if len(commit.Steps) != 1 || commit.Steps[0].Mode != StepWriteUpdate || commit.Steps[0].Step.Status != testCase.stepStatus {
				t.Fatalf("step writes = %#v, want status %q", commit.Steps, testCase.stepStatus)
			}
			if len(commit.Events) != 2 || commit.Events[0].Type != testCase.nodeEvent || commit.Events[1].Type != testCase.runEvent {
				t.Fatalf("events = %#v, want %q before %q", commit.Events, testCase.nodeEvent, testCase.runEvent)
			}
		})
	}
}

func TestRuntimeStoreCommitRejectsStepWithoutRun(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		new  func(*testing.T) commitReader
	}{
		{name: "memory", new: func(*testing.T) commitReader { return NewMemoryRuntimeStore() }},
	}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			store := testCase.new(t)
			step := StepRecord{RunID: "missing-run", StepID: "orphan-step", Status: StepStatusRunning}
			_, err := store.Commit(context.Background(), Commit{
				Steps: []StepWrite{{Mode: StepWriteAppend, Step: step}},
			})
			if !errors.Is(err, ErrRunnerRecordNotFound) {
				t.Fatalf("Commit() error = %v, want record not found", err)
			}
		})
	}
}

func TestRuntimeStoreCommitRejectsOrphanCheckpointAndEvent(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		new  func(*testing.T) checkpointEventCommitReader
	}{
		{name: "memory", new: func(*testing.T) checkpointEventCommitReader { return NewMemoryRuntimeStore() }},
	}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			t.Run("checkpoint", func(t *testing.T) {
				store := testCase.new(t)
				checkpoint := CheckpointRecord{RunID: "missing-run", CheckpointID: "orphan-checkpoint"}
				_, err := store.Commit(context.Background(), Commit{
					Checkpoints: []CheckpointWrite{{Record: checkpoint, Payload: []byte("payload")}},
				})
				if !errors.Is(err, ErrRunnerRecordNotFound) {
					t.Fatalf("Commit() error = %v, want record not found", err)
				}
				if _, _, err := store.Load(context.Background(), checkpoint.CheckpointID); !errors.Is(err, ErrRunnerRecordNotFound) {
					t.Fatalf("Load() error = %v, want record not found", err)
				}
			})

			t.Run("event", func(t *testing.T) {
				store := testCase.new(t)
				event := Event{ID: "orphan-event", RunID: "missing-run", Type: EventNodeFinished}
				_, err := store.Commit(context.Background(), Commit{Events: []Event{event}})
				if !errors.Is(err, ErrRunnerRecordNotFound) {
					t.Fatalf("Commit() error = %v, want record not found", err)
				}
				events, listErr := store.ListEvents(event.RunID)
				if listErr != nil {
					t.Fatalf("ListEvents() error = %v", listErr)
				}
				if len(events) != 0 {
					t.Fatalf("orphan events persisted: %#v", events)
				}
			})

			t.Run("late orphan event rolls back records", func(t *testing.T) {
				store := testCase.new(t)
				run := RunRecord{RunID: "run-before-orphan-event", Status: RunStatusRunning}
				if _, err := store.Commit(context.Background(), Commit{Run: &RunWrite{Mode: RunWriteCreate, Run: run}}); err != nil {
					t.Fatalf("create Run commit: %v", err)
				}
				checkpoint := CheckpointRecord{RunID: run.RunID, CheckpointID: "checkpoint-before-orphan-event"}
				validEvent := Event{ID: "event-before-orphan-event", RunID: run.RunID, Type: EventNodeFinished}
				orphanEvent := Event{ID: "orphan-event-after-valid-records", RunID: "missing-run", Type: EventNodeFinished}
				_, err := store.Commit(context.Background(), Commit{
					Checkpoints: []CheckpointWrite{{Record: checkpoint, Payload: []byte("payload")}},
					Events:      []Event{validEvent, orphanEvent},
				})
				if !errors.Is(err, ErrRunnerRecordNotFound) {
					t.Fatalf("Commit() error = %v, want record not found", err)
				}
				if _, _, err := store.Load(context.Background(), checkpoint.CheckpointID); !errors.Is(err, ErrRunnerRecordNotFound) {
					t.Fatalf("Load() error = %v, want record not found", err)
				}
				events, listErr := store.ListEvents(run.RunID)
				if listErr != nil {
					t.Fatalf("ListEvents() error = %v", listErr)
				}
				if len(events) != 0 {
					t.Fatalf("records survived orphan event failure: %#v", events)
				}
			})

			t.Run("same transaction create", func(t *testing.T) {
				store := testCase.new(t)
				run := RunRecord{RunID: "run-created-with-records", Status: RunStatusRunning}
				checkpoint := CheckpointRecord{RunID: run.RunID, CheckpointID: "checkpoint-created-with-run"}
				event := Event{ID: "event-created-with-run", RunID: run.RunID, Type: EventRunStarted}
				if _, err := store.Commit(context.Background(), Commit{
					Run:         &RunWrite{Mode: RunWriteCreate, Run: run},
					Checkpoints: []CheckpointWrite{{Record: checkpoint, Payload: []byte("payload")}},
					Events:      []Event{event},
				}); err != nil {
					t.Fatalf("Commit() error = %v", err)
				}
				_, payload, err := store.Load(context.Background(), checkpoint.CheckpointID)
				if err != nil {
					t.Fatalf("Load() error = %v", err)
				}
				if string(payload) != "payload" {
					t.Fatalf("checkpoint payload = %q, want payload", payload)
				}
				events, err := store.ListEvents(run.RunID)
				if err != nil {
					t.Fatalf("ListEvents() error = %v", err)
				}
				if len(events) != 1 || events[0].ID != event.ID {
					t.Fatalf("events = %#v, want %#v", events, []Event{event})
				}
			})
		})
	}
}

func TestMemoryRuntimeStoreCommitRollsBackEveryRecordOnLateFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	runtimeStore := NewMemoryRuntimeStore()
	original := RunRecord{RunID: "run-memory-atomic", Status: RunStatusPending}
	if err := runtimeStore.CreateRun(ctx, original); err != nil {
		t.Fatalf("CreateRun(): %v", err)
	}

	updated := original
	updated.Status = RunStatusRunning
	step := StepRecord{RunID: original.RunID, StepID: "step-memory-atomic", Status: StepStatusSucceeded}
	checkpoint := CheckpointRecord{
		RunID: original.RunID, CheckpointID: "checkpoint-memory-atomic", StepID: step.StepID,
		Stage: CheckpointAfterNode,
	}
	commit := Commit{
		Run:         &RunWrite{Mode: RunWriteUpdate, Run: updated},
		Steps:       []StepWrite{{Mode: StepWriteAppend, Step: step}},
		Checkpoints: []CheckpointWrite{{Record: checkpoint, Payload: []byte("checkpoint")}},
		Events: []Event{
			{ID: "event-memory-valid", RunID: original.RunID, Type: EventNodeFinished},
			{ID: "event-memory-invalid", RunID: "../invalid", Type: EventRunFinished},
		},
	}
	if _, err := runtimeStore.Commit(ctx, commit); err == nil {
		t.Fatal("Commit() error = nil")
	}

	persisted, err := runtimeStore.GetRun(ctx, original.RunID)
	if err != nil {
		t.Fatalf("GetRun(): %v", err)
	}
	if persisted.Revision != original.Revision || persisted.Status != original.Status {
		t.Fatalf("run changed after rollback: %#v", persisted)
	}
	if _, err := runtimeStore.GetStep(ctx, step.StepID); !errors.Is(err, ErrRunnerRecordNotFound) {
		t.Fatalf("GetStep() error = %v, want record not found", err)
	}
	if _, _, err := runtimeStore.Load(ctx, checkpoint.CheckpointID); !errors.Is(err, ErrRunnerRecordNotFound) {
		t.Fatalf("Load() error = %v, want record not found", err)
	}
	events, err := runtimeStore.ListEvents(original.RunID)
	if err != nil {
		t.Fatalf("ListEvents(): %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events survived rollback: %#v", events)
	}
}

func TestRuntimeStoreCommitRejectsTombstonedRunAtomically(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		new  func(*testing.T) deletionAwareCommitStore
	}{
		{name: "memory", new: func(*testing.T) deletionAwareCommitStore { return NewMemoryRuntimeStore() }},
	}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := testCase.new(t)
			run := RunRecord{RunID: "run-tombstoned-commit", RootRunID: "run-tombstoned-commit", Status: RunStatusCompleted}
			if err := store.CreateRun(ctx, run); err != nil {
				t.Fatalf("CreateRun(): %v", err)
			}
			deletionID := "deletion-tombstoned-commit"
			run.Deletion = &RunDeletionState{ID: deletionID, RootRunID: run.RunID, Phase: RunDeletionReserved}
			reserved, err := store.CompareAndSwapRun(withRunDeletionMutation(ctx, deletionID), run.Revision, run)
			if err != nil {
				t.Fatalf("reserve deletion: %v", err)
			}
			step := StepRecord{RunID: run.RunID, StepID: "step-tombstoned-commit", Status: StepStatusSucceeded}
			checkpoint := CheckpointRecord{RunID: run.RunID, CheckpointID: "checkpoint-tombstoned-commit", StepID: step.StepID, Stage: CheckpointAfterNode}
			_, err = store.Commit(ctx, Commit{
				Steps:       []StepWrite{{Mode: StepWriteAppend, Step: step}},
				Checkpoints: []CheckpointWrite{{Record: checkpoint, Payload: []byte("payload")}},
				Events:      []Event{{ID: "event-tombstoned-commit", RunID: run.RunID, Type: EventRunFinished}},
			})
			if !errors.Is(err, ErrRunControlNotAllowed) {
				t.Fatalf("Commit() error = %v, want control not allowed", err)
			}
			persisted, err := store.GetRun(ctx, run.RunID)
			if err != nil {
				t.Fatalf("GetRun(): %v", err)
			}
			if persisted.Revision != reserved.Revision || persisted.Deletion == nil || persisted.Deletion.ID != deletionID {
				t.Fatalf("persisted run = %#v, want unchanged reservation %#v", persisted, reserved)
			}
			if _, err := store.GetStep(ctx, step.StepID); !errors.Is(err, ErrRunnerRecordNotFound) {
				t.Fatalf("GetStep() error = %v, want not found", err)
			}
			if _, _, err := store.Load(ctx, checkpoint.CheckpointID); !errors.Is(err, ErrRunnerRecordNotFound) {
				t.Fatalf("Load() error = %v, want not found", err)
			}
			events, err := store.ListEvents(run.RunID)
			if err != nil {
				t.Fatalf("ListEvents(): %v", err)
			}
			if len(events) != 0 {
				t.Fatalf("events survived rejected commit: %#v", events)
			}
		})
	}
}

func TestRuntimeStoreDeletionFenceRejectsRunRecreation(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		new  func(*testing.T) deletionAwareCommitStore
	}{
		{name: "memory", new: func(*testing.T) deletionAwareCommitStore { return NewMemoryRuntimeStore() }},
	}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := testCase.new(t)
			run := RunRecord{RunID: "run-recreate-fenced", RootRunID: "run-recreate-fenced", Status: RunStatusCompleted}
			if err := store.CreateRun(ctx, run); err != nil {
				t.Fatalf("CreateRun(): %v", err)
			}
			coordinator := NewRunDeletionCoordinator(store, store, store, nil)
			if err := coordinator.DeleteRun(ctx, run.RunID); err != nil {
				t.Fatalf("DeleteRun(): %v", err)
			}
			_, err := store.Commit(ctx, Commit{Run: &RunWrite{Mode: RunWriteCreate, Run: run}})
			if !errors.Is(err, ErrRunControlNotAllowed) {
				t.Fatalf("Commit(recreate) error = %v, want control not allowed", err)
			}
			if _, err := store.GetRun(ctx, run.RunID); !errors.Is(err, ErrRunnerRecordNotFound) {
				t.Fatalf("GetRun() error = %v, want not found", err)
			}
		})
	}
}
