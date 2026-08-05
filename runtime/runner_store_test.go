package runtime

import (
	"context"
	"encoding/json"
	"errors"
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

	page, err := sink.ListEventPage(runID, "", 1)
	if err != nil {
		t.Fatalf("list event page: %v", err)
	}
	if len(page.Items) != 1 || string(page.Items[0].Payload) != string(payload) {
		t.Fatal("large payload mismatch after paginated reload")
	}
	if page.NextCursor != "" {
		t.Fatalf("next cursor = %q, want empty", page.NextCursor)
	}
}

func TestFileEventSinkListEventPageReadsNewestFirstAcrossPages(t *testing.T) {
	t.Parallel()

	sink := NewFileEventSink(t.TempDir())
	runID := "run-paginated"
	for index := 0; index < 5; index++ {
		if err := sink.Publish(context.Background(), Event{
			ID:    string(rune('a' + index)),
			RunID: runID,
			Type:  EventRunStarted,
		}); err != nil {
			t.Fatalf("publish event %d: %v", index, err)
		}
	}

	first, err := sink.ListEventPage(runID, "", 2)
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	assertEventIDs(t, first.Items, "e", "d")
	if first.NextCursor == "" {
		t.Fatal("first page next cursor is empty")
	}

	second, err := sink.ListEventPage(runID, first.NextCursor, 2)
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	assertEventIDs(t, second.Items, "c", "b")
	if second.NextCursor == "" {
		t.Fatal("second page next cursor is empty")
	}

	third, err := sink.ListEventPage(runID, second.NextCursor, 2)
	if err != nil {
		t.Fatalf("list third page: %v", err)
	}
	assertEventIDs(t, third.Items, "a")
	if third.NextCursor != "" {
		t.Fatalf("third page next cursor = %q, want empty", third.NextCursor)
	}
}

func TestFileEventSinkListEventPageRejectsInvalidCursor(t *testing.T) {
	t.Parallel()

	sink := NewFileEventSink(t.TempDir())
	if err := sink.Publish(context.Background(), Event{ID: "event-1", RunID: "run-1", Type: EventRunStarted}); err != nil {
		t.Fatalf("publish event: %v", err)
	}
	if _, err := sink.ListEventPage("run-1", "1", 10); !errors.Is(err, ErrInvalidEventCursor) {
		t.Fatalf("ListEventPage() error = %v, want ErrInvalidEventCursor", err)
	}
}

func TestFileEventSinkListEventPageReturnsEmptyPageForMissingRun(t *testing.T) {
	t.Parallel()

	page, err := NewFileEventSink(t.TempDir()).ListEventPage("missing", "", 10)
	if err != nil {
		t.Fatalf("ListEventPage() error = %v", err)
	}
	if len(page.Items) != 0 || page.NextCursor != "" {
		t.Fatalf("page = %#v, want empty", page)
	}
}

func assertEventIDs(t *testing.T, events []Event, want ...string) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("event count = %d, want %d; events = %#v", len(events), len(want), events)
	}
	for index, event := range events {
		if event.ID != want[index] {
			t.Fatalf("event %d id = %q, want %q", index, event.ID, want[index])
		}
	}
}
