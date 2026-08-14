package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type cancelAfterCommitTransactionStore struct {
	store   TransactionStore
	cancel  context.CancelFunc
	commits int
}

func (store *cancelAfterCommitTransactionStore) Commit(ctx context.Context, commit Commit) (CommitResult, error) {
	store.commits++
	result, err := store.store.Commit(ctx, commit)
	if err == nil {
		store.cancel()
	}
	return result, err
}

type committedObserverSink struct {
	calls      int
	contextErr error
	err        error
}

func (sink *committedObserverSink) Publish(ctx context.Context, _ Event) error {
	sink.calls++
	sink.contextErr = ctx.Err()
	return sink.err
}

func (sink *committedObserverSink) PublishBatch(ctx context.Context, events []Event) error {
	for _, event := range events {
		if err := sink.Publish(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func TestAnalyzeRunEventsKeepsPausedRunOpenAndTracksCanceledNodes(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	pausedAt := startedAt.Add(time.Second)
	paused := AnalyzeRunEvents("paused-run", []Event{
		{RunID: "paused-run", Type: EventRunStarted, Timestamp: startedAt},
		{RunID: "paused-run", Type: EventRunPaused, Timestamp: pausedAt},
	})
	if paused.Status != RunStatusPaused || !paused.FinishedAt.IsZero() || paused.Duration != time.Second {
		t.Fatalf("paused analysis = %#v", paused)
	}

	canceledAt := startedAt.Add(2 * time.Second)
	canceled := AnalyzeRunEvents("canceled-run", []Event{
		{RunID: "canceled-run", StepID: "step-1", NodeID: "work", Type: EventNodeStarted, Timestamp: startedAt},
		{RunID: "canceled-run", StepID: "step-1", NodeID: "work", Type: EventNodeCanceled, Timestamp: canceledAt, Payload: json.RawMessage(`{"attempt":2}`)},
	})
	if len(canceled.Nodes) != 1 || canceled.Nodes[0].Canceled != 1 || canceled.Nodes[0].Failed != 0 || canceled.Nodes[0].AttemptCount != 2 || canceled.Nodes[0].Duration != 2*time.Second {
		t.Fatalf("canceled node analysis = %#v", canceled.Nodes)
	}
}

func TestGraphRunnerPublishEventNotifiesSynchronousObserverAfterSink(t *testing.T) {
	t.Parallel()

	sink := &captureEventSink{}
	observerErr := errors.New("observer failed")
	var observed Event
	ctx := WithRunnerEventObserver(context.Background(), EventObserverFunc(func(_ context.Context, event Event) error {
		if len(sink.events) != 1 {
			t.Fatalf("observer ran before event sink: events = %#v", sink.events)
		}
		observed = event
		return observerErr
	}))
	runner := &GraphRunner{
		eventSink: sink,
		now:       func() time.Time { return time.Unix(123, 0).UTC() },
	}

	err := runner.publishEvent(ctx, RunRecord{RunID: "run-1"}, "step-1", "node-1", EventLLMContentChunk, map[string]any{"text": "hello"})
	if !errors.Is(err, observerErr) {
		t.Fatalf("publishEvent() error = %v, want %v", err, observerErr)
	}
	if observed.RunID != "run-1" || observed.StepID != "step-1" || observed.NodeID != "node-1" || observed.Type != EventLLMContentChunk {
		t.Fatalf("observed event = %#v", observed)
	}
}

func TestGraphRunnerCommitKeepsObserverFailuresOutsideTransactionResult(t *testing.T) {
	t.Parallel()

	persistentStore := NewMemoryRuntimeStore()
	commitCtx, cancel := context.WithCancel(context.Background())
	transactionStore := &cancelAfterCommitTransactionStore{store: persistentStore, cancel: cancel}
	sinkErr := errors.New("event sink observer failed")
	sink := &committedObserverSink{err: sinkErr}
	contextObserverErr := errors.New("context observer failed")
	contextObserverCalls := 0
	ctx := WithRunnerEventObserver(commitCtx, EventObserverFunc(func(observerCtx context.Context, _ Event) error {
		contextObserverCalls++
		if err := observerCtx.Err(); err != nil {
			t.Fatalf("committed context observer received canceled context: %v", err)
		}
		return contextObserverErr
	}))
	runner := &GraphRunner{transactionStore: transactionStore, eventSink: sink}
	run := RunRecord{RunID: "run-committed-observer", Status: RunStatusRunning}
	event := Event{ID: "event-committed-observer", RunID: run.RunID, Type: EventRunStarted}

	result, err := runner.commitRuntime(ctx, Commit{
		Run:    &RunWrite{Mode: RunWriteCreate, Run: run},
		Events: []Event{event},
	})
	if err != nil {
		t.Fatalf("commitRuntime() error = %v", err)
	}
	if result.Run == nil || result.Run.RunID != run.RunID {
		t.Fatalf("commit result = %#v", result.Run)
	}
	if transactionStore.commits != 1 {
		t.Fatalf("transaction commits = %d, want 1", transactionStore.commits)
	}
	if sink.calls != 1 || sink.contextErr != nil {
		t.Fatalf("event sink calls = %d, context error = %v", sink.calls, sink.contextErr)
	}
	if contextObserverCalls != 1 {
		t.Fatalf("context observer calls = %d, want 1", contextObserverCalls)
	}
	persistedEvents, err := persistentStore.ListEvents(run.RunID)
	if err != nil {
		t.Fatalf("ListEvents(): %v", err)
	}
	if len(persistedEvents) != 1 || persistedEvents[0].ID != event.ID {
		t.Fatalf("persisted events = %#v", persistedEvents)
	}
}

func TestGraphRunnerCommitDoesNotObserveRejectedTransaction(t *testing.T) {
	t.Parallel()

	storeErr := errors.New("transaction rejected")
	sink := &committedObserverSink{}
	observerCalls := 0
	ctx := WithRunnerEventObserver(context.Background(), EventObserverFunc(func(context.Context, Event) error {
		observerCalls++
		return nil
	}))
	runner := &GraphRunner{
		transactionStore: failingRuntimeTransactionStore{err: storeErr},
		eventSink:        sink,
	}
	_, err := runner.commitRuntime(ctx, Commit{Events: []Event{{ID: "not-committed", RunID: "run", Type: EventRunStarted}}})
	if !errors.Is(err, storeErr) {
		t.Fatalf("commitRuntime() error = %v, want %v", err, storeErr)
	}
	if sink.calls != 0 || observerCalls != 0 {
		t.Fatalf("rejected commit observer calls = sink %d context %d", sink.calls, observerCalls)
	}
}

func TestEventAnalyzerPublishBatchPreservesOrderAndCopiesPayloads(t *testing.T) {
	t.Parallel()

	payload := json.RawMessage(`"a"`)
	events := []Event{
		{ID: "first", RunID: "run", Type: EventRunStarted, Payload: payload},
		{ID: "second", RunID: "run", Type: EventRunFinished},
	}
	analyzer := NewEventAnalyzer()
	if err := analyzer.PublishBatch(context.Background(), events); err != nil {
		t.Fatalf("PublishBatch() error = %v", err)
	}
	copy(payload, []byte(`"b"`))

	stored, err := analyzer.ListEvents("run")
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(stored) != 2 || stored[0].ID != "first" || stored[1].ID != "second" {
		t.Fatalf("ListEvents() = %#v, want original batch order", stored)
	}
	if string(stored[0].Payload) != `"a"` {
		t.Fatalf("stored payload = %s, want copied original", stored[0].Payload)
	}
}
