package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

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
