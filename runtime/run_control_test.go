package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type runControlEventSink struct {
	events []Event
	err    error
	calls  int
}

func (s *runControlEventSink) Publish(_ context.Context, event Event) error {
	s.calls++
	if s.err != nil {
		return s.err
	}
	s.events = append(s.events, event)
	return nil
}

func (s *runControlEventSink) PublishBatch(ctx context.Context, events []Event) error {
	for _, event := range events {
		if err := s.Publish(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

type failingRuntimeTransactionStore struct {
	err error
}

func (s failingRuntimeTransactionStore) Commit(context.Context, Commit) (CommitResult, error) {
	return CommitResult{}, s.err
}

func (s failingRuntimeTransactionStore) ResolveCommit(_ context.Context, transactionID string) (CommitResult, error) {
	return CommitResult{TransactionID: transactionID, Outcome: TransactionNotStarted}, nil
}

type runControlHookTransactionStore struct {
	TransactionStore
	once              sync.Once
	beforeFirstUpdate func(context.Context) error
	hookErr           error
}

func (store *runControlHookTransactionStore) Commit(ctx context.Context, commit Commit) (CommitResult, error) {
	if commit.Run != nil && commit.Run.Mode == RunWriteUpdate && store.beforeFirstUpdate != nil {
		store.once.Do(func() {
			store.hookErr = store.beforeFirstUpdate(ctx)
		})
		if store.hookErr != nil {
			return CommitResult{}, store.hookErr
		}
	}
	return store.TransactionStore.Commit(ctx, commit)
}

type runControlRecordingDeleter struct {
	runID string
}

func (deleter *runControlRecordingDeleter) DeleteRun(_ context.Context, runID string) error {
	deleter.runID = runID
	return nil
}

func TestRunControlServiceCancelsPausedRunAndPublishesOrderedEvents(t *testing.T) {
	store := NewMemoryRuntimeStore()
	startedAt := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	run := RunRecord{RunID: "run-1", GraphID: "graph-1", GraphSessionID: "session-1", CurrentNodeID: "node-1", Status: RunStatusPaused, PauseRequested: true, UpdatedAt: startedAt}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	sink := &runControlEventSink{}
	control, err := NewRunControlService(store, store, sink, nil)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	now := startedAt.Add(time.Minute)
	control, err = control.WithNow(func() time.Time { return now })
	if err != nil {
		t.Fatalf("set clock: %v", err)
	}

	canceled, err := control.CancelPausedRun(context.Background(), run.RunID)
	if err != nil {
		t.Fatalf("cancel paused run: %v", err)
	}
	if canceled.Status != RunStatusCanceled || canceled.PauseRequested || canceled.CancelRequested || canceled.FinishedAt == nil || !canceled.FinishedAt.Equal(now) {
		t.Fatalf("canceled run = %#v", canceled)
	}
	if len(sink.events) != 2 || sink.events[0].Type != EventRunCancelRequested || sink.events[1].Type != EventRunCanceled {
		t.Fatalf("events = %#v", sink.events)
	}
	for _, event := range sink.events {
		if event.GraphID != run.GraphID || event.GraphSessionID != run.GraphSessionID || event.RunID != run.RunID || event.NodeID != run.CurrentNodeID || !event.Timestamp.Equal(now) {
			t.Fatalf("event metadata = %#v", event)
		}
	}

	repeated, err := control.CancelPausedRun(context.Background(), run.RunID)
	if err != nil || repeated.Status != RunStatusCanceled {
		t.Fatalf("repeat cancel = %#v, err=%v", repeated, err)
	}
	if len(sink.events) != 2 {
		t.Fatalf("repeat cancel published events: %#v", sink.events)
	}
}

func TestRunControlServiceMarksLostExecutionFailed(t *testing.T) {
	store := NewMemoryRuntimeStore()
	run := RunRecord{RunID: "run-1", GraphID: "graph-1", Status: RunStatusRunning}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	sink := &runControlEventSink{}
	control, err := NewRunControlService(store, store, sink, nil)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	failed, err := control.MarkRunExecutionLost(context.Background(), run.RunID)
	if err != nil {
		t.Fatalf("mark execution lost: %v", err)
	}
	if failed.Status != RunStatusFailed || failed.ErrorCode != "run_execution_lost" || failed.FinishedAt == nil {
		t.Fatalf("failed run = %#v", failed)
	}
	if len(sink.events) != 1 || sink.events[0].Type != EventRunFailed {
		t.Fatalf("events = %#v", sink.events)
	}
}

func TestRunControlServicePreservesValidExecutionLease(t *testing.T) {
	t.Parallel()

	store := NewMemoryRuntimeStore()
	now := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	run := RunRecord{
		RunID: "leased-run", GraphID: "graph-1", Status: RunStatusRunning,
		ExecutionLease: &ExecutionLease{
			OwnerID: "other-server", Token: "token", Epoch: 4, Status: ExecutionLeaseActive,
			AcquiredAt: now.Add(-time.Minute), HeartbeatAt: now, ExpiresAt: now.Add(time.Minute),
		},
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	control, err := NewRunControlService(store, store, &runControlEventSink{}, nil)
	if err != nil {
		t.Fatalf("NewRunControlService() error = %v", err)
	}
	control, err = control.WithNow(func() time.Time { return now })
	if err != nil {
		t.Fatalf("WithNow() error = %v", err)
	}
	persisted, err := control.MarkRunExecutionLost(context.Background(), run.RunID)
	if !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("MarkRunExecutionLost() error = %v, want control not allowed", err)
	}
	if persisted.Status != RunStatusRunning || persisted.ExecutionLease == nil || persisted.ExecutionLease.Epoch != 4 {
		t.Fatalf("persisted leased run = %#v", persisted)
	}
}

func TestRunControlServiceReleasesExpiredLeaseBeforeMarkingExecutionLost(t *testing.T) {
	t.Parallel()

	store := NewMemoryRuntimeStore()
	now := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	run := RunRecord{
		RunID: "expired-lease-run", GraphID: "graph-1", Status: RunStatusRunning,
		ExecutionLease: &ExecutionLease{
			OwnerID: "stopped-server", Token: "token", Epoch: 3, Status: ExecutionLeaseActive,
			AcquiredAt: now.Add(-2 * time.Minute), HeartbeatAt: now.Add(-2 * time.Minute), ExpiresAt: now.Add(-time.Minute),
		},
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	control, err := NewRunControlService(store, store, store, nil)
	if err != nil {
		t.Fatalf("NewRunControlService() error = %v", err)
	}
	control, err = control.WithNow(func() time.Time { return now })
	if err != nil {
		t.Fatalf("WithNow() error = %v", err)
	}
	failed, err := control.MarkRunExecutionLost(context.Background(), run.RunID)
	if err != nil {
		t.Fatalf("MarkRunExecutionLost() error = %v", err)
	}
	if failed.Status != RunStatusFailed || failed.ErrorCode != "run_execution_lost" {
		t.Fatalf("failed run = %#v", failed)
	}
	if failed.ExecutionLease == nil || failed.ExecutionLease.Status != ExecutionLeaseReleased || failed.ExecutionLease.Epoch != 3 {
		t.Fatalf("released expired lease = %#v", failed.ExecutionLease)
	}
}

func TestRunControlServiceReevaluatesStatusAfterRevisionConflict(t *testing.T) {
	t.Parallel()

	t.Run("lost execution preserves concurrent completion", func(t *testing.T) {
		baseStore := NewMemoryRuntimeStore()
		run := RunRecord{RunID: "run-completed", GraphID: "graph-1", Status: RunStatusRunning}
		if err := baseStore.CreateRun(context.Background(), run); err != nil {
			t.Fatal(err)
		}
		finishedAt := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
		hookStore := &runControlHookTransactionStore{TransactionStore: baseStore}
		hookStore.beforeFirstUpdate = func(ctx context.Context) error {
			current, err := baseStore.GetRun(ctx, run.RunID)
			if err != nil {
				return err
			}
			current.Status = RunStatusCompleted
			current.UpdatedAt = finishedAt
			current.FinishedAt = &finishedAt
			_, err = baseStore.CompareAndSwapRun(ctx, current.Revision, current)
			return err
		}
		sink := &runControlEventSink{}
		control, err := NewRunControlService(baseStore, hookStore, sink, nil)
		if err != nil {
			t.Fatal(err)
		}

		completed, err := control.MarkRunExecutionLost(context.Background(), run.RunID)
		if err != nil {
			t.Fatalf("MarkRunExecutionLost() error = %v", err)
		}
		if completed.Status != RunStatusCompleted || completed.ErrorCode != "" {
			t.Fatalf("run = %#v, want concurrent completion", completed)
		}
		if len(sink.events) != 0 {
			t.Fatalf("events = %#v, want none", sink.events)
		}
	})

	t.Run("paused cancel preserves concurrent resume", func(t *testing.T) {
		baseStore := NewMemoryRuntimeStore()
		run := RunRecord{RunID: "run-resumed", GraphID: "graph-1", Status: RunStatusPaused, PauseRequested: true}
		if err := baseStore.CreateRun(context.Background(), run); err != nil {
			t.Fatal(err)
		}
		hookStore := &runControlHookTransactionStore{TransactionStore: baseStore}
		hookStore.beforeFirstUpdate = func(ctx context.Context) error {
			current, err := baseStore.GetRun(ctx, run.RunID)
			if err != nil {
				return err
			}
			current.Status = RunStatusRunning
			current.PauseRequested = false
			current.UpdatedAt = time.Date(2026, 8, 17, 9, 1, 0, 0, time.UTC)
			_, err = baseStore.CompareAndSwapRun(ctx, current.Revision, current)
			return err
		}
		sink := &runControlEventSink{}
		control, err := NewRunControlService(baseStore, hookStore, sink, nil)
		if err != nil {
			t.Fatal(err)
		}

		_, err = control.CancelPausedRun(context.Background(), run.RunID)
		if !errors.Is(err, ErrRunControlNotAllowed) {
			t.Fatalf("CancelPausedRun() error = %v, want run control rejection", err)
		}
		persisted, getErr := baseStore.GetRun(context.Background(), run.RunID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if persisted.Status != RunStatusRunning || persisted.PauseRequested {
			t.Fatalf("run = %#v, want concurrent resume", persisted)
		}
		if len(sink.events) != 0 {
			t.Fatalf("events = %#v, want none", sink.events)
		}
	})
}

func TestRunControlServiceNormalizesRunIDBeforeDeletion(t *testing.T) {
	store := NewMemoryRuntimeStore()
	run := RunRecord{RunID: "run-1", Status: RunStatusCompleted}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	deleter := &runControlRecordingDeleter{}
	control, err := NewRunControlService(store, store, nil, deleter)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	deleted, err := control.DeleteRun(context.Background(), " run-1 ")
	if err != nil {
		t.Fatalf("delete run: %v", err)
	}
	if deleted.RunID != run.RunID || deleter.runID != run.RunID {
		t.Fatalf("deleted run = %#v, deleter run ID = %q", deleted, deleter.runID)
	}
}

func TestRunControlServiceSeparatesStoreFailuresFromCommittedEventObserverFailures(t *testing.T) {
	baseStore := NewMemoryRuntimeStore()
	run := RunRecord{RunID: "run-1", Status: RunStatusPaused}
	if err := baseStore.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	storeErr := errors.New("store write failed")
	control, err := NewRunControlService(baseStore, failingRuntimeTransactionStore{err: storeErr}, &runControlEventSink{}, nil)
	if err != nil {
		t.Fatalf("new store failure service: %v", err)
	}
	if _, err := control.CancelPausedRun(context.Background(), run.RunID); !errors.Is(err, storeErr) {
		t.Fatalf("store failure = %v", err)
	}

	eventErr := errors.New("event publish failed")
	eventSink := &runControlEventSink{err: eventErr}
	control, err = NewRunControlService(baseStore, baseStore, eventSink, nil)
	if err != nil {
		t.Fatalf("new event failure service: %v", err)
	}
	canceled, err := control.CancelPausedRun(context.Background(), run.RunID)
	if err != nil {
		t.Fatalf("committed event observer failure leaked from CancelPausedRun(): %v", err)
	}
	if canceled.Status != RunStatusCanceled || eventSink.calls != 1 {
		t.Fatalf("cancel result = %#v, observer calls = %d", canceled, eventSink.calls)
	}
	persisted, err := baseStore.GetRun(context.Background(), run.RunID)
	if err != nil {
		t.Fatalf("get persisted run: %v", err)
	}
	if persisted.Status != RunStatusCanceled || persisted.CancelRequested {
		t.Fatalf("persisted event-failure state = %#v", persisted)
	}
	events, err := baseStore.ListEvents(run.RunID)
	if err != nil {
		t.Fatalf("list persisted events: %v", err)
	}
	if len(events) != 2 || events[0].Type != EventRunCancelRequested || events[1].Type != EventRunCanceled {
		t.Fatalf("persisted event-failure events = %#v", events)
	}
}

func TestRunControlServiceCancelPausedRunCascadesThroughPersistedChildren(t *testing.T) {
	store := NewMemoryRuntimeStore()
	runs := []RunRecord{
		{RunID: "parent", RootRunID: "parent", RunPath: []string{"parent"}, Status: RunStatusPaused, ChildRunIDs: []string{"child"}},
		{RunID: "child", ParentRunID: "parent", RootRunID: "parent", RunPath: []string{"parent", "child"}, Status: RunStatusPaused, ChildRunIDs: []string{"grandchild"}},
		{RunID: "grandchild", ParentRunID: "child", RootRunID: "parent", RunPath: []string{"parent", "child", "grandchild"}, Status: RunStatusPaused},
	}
	for _, run := range runs {
		if err := store.CreateRun(context.Background(), run); err != nil {
			t.Fatalf("create run %q: %v", run.RunID, err)
		}
	}
	sink := &runControlEventSink{}
	control, err := NewRunControlService(store, store, sink, nil)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	canceled, err := control.CancelPausedRun(context.Background(), "parent")
	if err != nil {
		t.Fatalf("cancel parent: %v", err)
	}
	if canceled.Status != RunStatusCanceled {
		t.Fatalf("parent status = %q, want canceled", canceled.Status)
	}
	for _, runID := range []string{"parent", "child", "grandchild"} {
		persisted, err := store.GetRun(context.Background(), runID)
		if err != nil {
			t.Fatalf("get run %q: %v", runID, err)
		}
		if persisted.Status != RunStatusCanceled {
			t.Fatalf("run %q status = %q, want canceled", runID, persisted.Status)
		}
	}
	wantRunIDs := []string{"grandchild", "grandchild", "child", "child", "parent", "parent"}
	wantTypes := []EventType{EventRunCancelRequested, EventRunCanceled, EventRunCancelRequested, EventRunCanceled, EventRunCancelRequested, EventRunCanceled}
	if len(sink.events) != len(wantTypes) {
		t.Fatalf("events = %#v", sink.events)
	}
	for index, event := range sink.events {
		if event.RunID != wantRunIDs[index] || event.Type != wantTypes[index] {
			t.Fatalf("event %d = %#v, want run %q type %q", index, event, wantRunIDs[index], wantTypes[index])
		}
	}
}
