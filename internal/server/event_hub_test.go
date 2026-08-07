package server

import (
	"context"
	"testing"

	"github.com/dengzii/weaveflow/runtime"
)

func TestEventHubReplaysEventsAfterCursor(t *testing.T) {
	hub := NewEventHub(1)
	for _, event := range []runtime.Event{
		{ID: "event-1", GraphID: "graph", RunID: "run", Type: runtime.EventType("run.started")},
		{ID: "event-2", GraphID: "graph", RunID: "run", Type: runtime.EventType("node.started")},
		{ID: "event-3", GraphID: "graph", RunID: "run", Type: runtime.EventType("run.finished")},
	} {
		if err := hub.Publish(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}

	events, unsubscribe := hub.Subscribe(eventFilter{GraphID: "graph"}, "event-1")
	defer unsubscribe()
	if event := <-events; event.ID != "event-2" {
		t.Fatalf("first replayed event = %q, want event-2", event.ID)
	}
	if event := <-events; event.ID != "event-3" {
		t.Fatalf("second replayed event = %q, want event-3", event.ID)
	}
}

func TestEventHubFiltersGraphAndSession(t *testing.T) {
	hub := NewEventHub(4)
	events, unsubscribe := hub.Subscribe(eventFilter{
		GraphID:        "graph-a",
		GraphSessionID: "session-a",
	})
	defer unsubscribe()

	for _, event := range []runtime.Event{
		{ID: "wrong-graph", GraphID: "graph-b", GraphSessionID: "session-a", RunID: "run"},
		{ID: "wrong-session", GraphID: "graph-a", GraphSessionID: "session-b", RunID: "run"},
		{ID: "matched", GraphID: "graph-a", GraphSessionID: "session-a", RunID: "run"},
	} {
		if err := hub.Publish(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}

	if event := <-events; event.ID != "matched" {
		t.Fatalf("filtered event = %q, want matched", event.ID)
	}
	select {
	case event := <-events:
		t.Fatalf("unexpected filtered event: %#v", event)
	default:
	}
}

func TestEventHubDisconnectsOverflowedSubscriber(t *testing.T) {
	hub := NewEventHub(1)
	events, unsubscribe := hub.Subscribe(eventFilter{})
	defer unsubscribe()

	if err := hub.Publish(context.Background(), runtime.Event{ID: "event-1"}); err != nil {
		t.Fatal(err)
	}
	if err := hub.Publish(context.Background(), runtime.Event{ID: "event-2"}); err != nil {
		t.Fatal(err)
	}
	if event, ok := <-events; !ok || event.ID != "event-1" {
		t.Fatalf("buffered event = %#v, open = %t", event, ok)
	}
	if _, ok := <-events; ok {
		t.Fatal("overflowed subscriber channel remained open")
	}
}
