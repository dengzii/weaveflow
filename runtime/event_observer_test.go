package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

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
