package runtime

import (
	"context"
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
		EventSink: sink,
		Now:       func() time.Time { return time.Unix(123, 0).UTC() },
	}

	err := runner.publishEvent(ctx, RunRecord{RunID: "run-1"}, "step-1", "node-1", EventLLMContentChunk, map[string]any{"text": "hello"})
	if !errors.Is(err, observerErr) {
		t.Fatalf("publishEvent() error = %v, want %v", err, observerErr)
	}
	if observed.RunID != "run-1" || observed.StepID != "step-1" || observed.NodeID != "node-1" || observed.Type != EventLLMContentChunk {
		t.Fatalf("observed event = %#v", observed)
	}
}
