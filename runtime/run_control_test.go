package runtime

import (
	"context"
	"errors"
	"testing"
	"time"
)

type runControlEventSink struct {
	events []Event
	err    error
}

func (s *runControlEventSink) Publish(_ context.Context, event Event) error {
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

func TestRunControlServiceReportsStoreAndEventFailures(t *testing.T) {
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
	control, err = NewRunControlService(baseStore, baseStore, &runControlEventSink{err: eventErr}, nil)
	if err != nil {
		t.Fatalf("new event failure service: %v", err)
	}
	if _, err := control.CancelPausedRun(context.Background(), run.RunID); !errors.Is(err, eventErr) {
		t.Fatalf("event failure = %v", err)
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
		{RunID: "parent", Status: RunStatusPaused, ChildRunIDs: []string{"child"}},
		{RunID: "child", ParentRunID: "parent", Status: RunStatusPaused, ChildRunIDs: []string{"grandchild"}},
		{RunID: "grandchild", ParentRunID: "child", Status: RunStatusPaused},
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
