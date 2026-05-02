package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestFileEventSinkListEventsSupportsLargePayloads(t *testing.T) {
	t.Parallel()

	sink := NewFileEventSink(t.TempDir())
	runID := "run-large-payload"
	largeText := strings.Repeat("x", 256*1024)
	payload, err := json.Marshal(map[string]string{"text": largeText})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	event := Event{
		RunID:   runID,
		Type:    EventLLMReasoning,
		Payload: payload,
	}
	if err := sink.Publish(context.Background(), event); err != nil {
		t.Fatalf("publish event: %v", err)
	}

	events, err := sink.ListEvents(runID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if string(events[0].Payload) != string(payload) {
		t.Fatalf("payload mismatch after reload")
	}
}
