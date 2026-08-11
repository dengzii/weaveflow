package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/runtime"
)

func TestEventHubReplaysPartitionedEventsAfterCursor(t *testing.T) {
	hub := newTestEventHub(eventHubOptions{eventHistoryLimit: 8, streamHistoryLimit: 8, maxReplay: 8})
	for _, event := range []runtime.Event{
		{ID: "event-1", GraphID: "graph", GraphSessionID: "session-a", RunID: "run", Type: runtime.EventRunStarted},
		{ID: "event-2", GraphID: "graph", GraphSessionID: "session-a", RunID: "run", Type: runtime.EventLLMContentChunk},
		{ID: "other-session", GraphID: "graph", GraphSessionID: "session-b", RunID: "run", Type: runtime.EventNodeStarted},
		{ID: "event-3", GraphID: "graph", GraphSessionID: "session-a", RunID: "run", Type: runtime.EventRunFinished},
	} {
		if err := hub.Publish(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}

	subscription := hub.Subscribe(eventFilter{GraphID: "graph", GraphSessionID: "session-a"}, "event-1")
	defer subscription.Unsubscribe()
	if subscription.Replay.Gap {
		t.Fatalf("replay = %#v, want cursor hit", subscription.Replay)
	}
	if event := <-subscription.Events; event.ID != "event-2" {
		t.Fatalf("first replayed event = %q, want event-2", event.ID)
	}
	if event := <-subscription.Events; event.ID != "event-3" {
		t.Fatalf("second replayed event = %q, want event-3", event.ID)
	}
	if metrics := hub.Metrics(); metrics.ReplayedEvents != 2 {
		t.Fatalf("replayed events = %d, want 2", metrics.ReplayedEvents)
	}
}

func TestEventHubReportsCursorGapAfterEviction(t *testing.T) {
	hub := newTestEventHub(eventHubOptions{eventHistoryLimit: 2, maxReplay: 2})
	for index := 1; index <= 3; index++ {
		publishTestEvent(t, hub, runtime.Event{
			ID:      "event-" + string(rune('0'+index)),
			GraphID: "graph",
			RunID:   "run",
			Type:    runtime.EventNodeStarted,
		})
	}
	partition := hub.partitions[eventPartitionKey{GraphID: "graph"}]
	if partition == nil {
		t.Fatal("graph history partition was not created")
	}
	if partition.regular.head == 0 {
		t.Fatalf("ring head = %d, want O(1) head advancement after eviction", partition.regular.head)
	}

	subscription := hub.Subscribe(eventFilter{GraphID: "graph"}, "event-1")
	defer subscription.Unsubscribe()
	if !subscription.Replay.Gap || subscription.Replay.Reason != "cursor_not_retained" {
		t.Fatalf("replay = %#v, want cursor_not_retained gap", subscription.Replay)
	}
	if subscription.Replay.OldestEventID != "event-2" || subscription.Replay.ResumeCursor != "event-3" {
		t.Fatalf("gap cursors = %#v", subscription.Replay)
	}
	select {
	case event := <-subscription.Events:
		t.Fatalf("unexpected replay after gap: %#v", event)
	default:
	}
}

func TestEventHubReportsGapForCursorAfterRestart(t *testing.T) {
	hub := newTestEventHub(eventHubOptions{})
	subscription := hub.Subscribe(eventFilter{GraphID: "graph"}, "event-before-restart")
	defer subscription.Unsubscribe()
	if !subscription.Replay.Gap || subscription.Replay.Reason != "cursor_not_retained" {
		t.Fatalf("replay = %#v, want restart gap", subscription.Replay)
	}
}

func TestEventHubKeepsGraphHistoriesIsolated(t *testing.T) {
	hub := newTestEventHub(eventHubOptions{eventHistoryLimit: 2, maxReplay: 4})
	publishTestEvent(t, hub, runtime.Event{ID: "graph-b-1", GraphID: "graph-b", RunID: "run", Type: runtime.EventRunStarted})
	publishTestEvent(t, hub, runtime.Event{ID: "graph-b-2", GraphID: "graph-b", RunID: "run", Type: runtime.EventRunFinished})
	for index := 0; index < 10; index++ {
		publishTestEvent(t, hub, runtime.Event{
			ID:      time.Unix(0, int64(index)).Format(time.RFC3339Nano),
			GraphID: "graph-a",
			RunID:   "run",
			Type:    runtime.EventNodeStarted,
		})
	}

	subscription := hub.Subscribe(eventFilter{GraphID: "graph-b"}, "graph-b-1")
	defer subscription.Unsubscribe()
	if subscription.Replay.Gap {
		t.Fatalf("graph A traffic expired graph B cursor: %#v", subscription.Replay)
	}
	if event := <-subscription.Events; event.ID != "graph-b-2" {
		t.Fatalf("graph B replay = %q, want graph-b-2", event.ID)
	}
}

func TestEventHubLimitsReplayAndStreamingRetention(t *testing.T) {
	now := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)
	hub := newTestEventHub(eventHubOptions{
		eventHistoryLimit:  8,
		streamHistoryLimit: 8,
		streamHistoryTTL:   time.Second,
		maxReplay:          1,
		now:                func() time.Time { return now },
	})
	publishTestEvent(t, hub, runtime.Event{ID: "start", GraphID: "graph", RunID: "run", Type: runtime.EventRunStarted})
	publishTestEvent(t, hub, runtime.Event{ID: "chunk", GraphID: "graph", RunID: "run", Type: runtime.EventLLMContentChunk})
	publishTestEvent(t, hub, runtime.Event{ID: "finish", GraphID: "graph", RunID: "run", Type: runtime.EventRunFinished})

	limited := hub.Subscribe(eventFilter{GraphID: "graph"}, "start")
	if !limited.Replay.Gap || limited.Replay.Reason != "replay_limit_exceeded" {
		t.Fatalf("limited replay = %#v", limited.Replay)
	}
	limited.Unsubscribe()

	now = now.Add(2 * time.Second)
	expired := hub.Subscribe(eventFilter{GraphID: "graph"}, "chunk")
	defer expired.Unsubscribe()
	if !expired.Replay.Gap {
		t.Fatalf("expired chunk replay = %#v, want gap", expired.Replay)
	}
}

func TestEventHubDisconnectsOverflowedSubscriber(t *testing.T) {
	hub := newTestEventHub(eventHubOptions{subscriberBuffer: 1})
	subscription := hub.Subscribe(eventFilter{}, "")
	defer subscription.Unsubscribe()

	publishTestEvent(t, hub, runtime.Event{ID: "event-1"})
	publishTestEvent(t, hub, runtime.Event{ID: "event-2"})
	if event, ok := <-subscription.Events; !ok || event.ID != "event-1" {
		t.Fatalf("buffered event = %#v, open = %t", event, ok)
	}
	if _, ok := <-subscription.Events; ok {
		t.Fatal("overflowed subscriber channel remained open")
	}
	closeState := <-subscription.Closed
	if closeState.Reason != "overflow" {
		t.Fatalf("close reason = %q, want overflow", closeState.Reason)
	}
	if metrics := hub.Metrics(); metrics.OverflowedSubscribers != 1 || metrics.CurrentSubscribers != 0 {
		t.Fatalf("metrics after overflow = %#v", metrics)
	}
}

func TestEventHubBoundsBytesAndReplacesOversizedPayload(t *testing.T) {
	hub := newTestEventHub(eventHubOptions{eventHistoryLimit: 8, eventHistoryBytes: 1024})
	subscription := hub.Subscribe(eventFilter{GraphID: "graph"}, "")
	defer subscription.Unsubscribe()
	publishTestEvent(t, hub, runtime.Event{
		ID:      "large",
		GraphID: "graph",
		RunID:   "run",
		Type:    runtime.EventNodeCustom,
		Payload: []byte(`{"value":"` + string(make([]byte, 2048)) + `"}`),
	})

	event := <-subscription.Events
	if len(event.Payload) >= 1024 || !jsonPayloadBool(event.Payload, "omitted") {
		t.Fatalf("oversized payload was not replaced: bytes=%d payload=%s", len(event.Payload), event.Payload)
	}
	metrics := hub.Metrics()
	if metrics.OversizedEvents != 1 || metrics.CurrentHistoryBytes > 1024 {
		t.Fatalf("bounded metrics = %#v", metrics)
	}
}

func TestEventHubKeepsChunkHistoryWithinByteLimit(t *testing.T) {
	hub := newTestEventHub(eventHubOptions{
		streamHistoryLimit: 128,
		streamHistoryBytes: 4 << 10,
	})
	for index := 0; index < 100; index++ {
		publishTestEvent(t, hub, runtime.Event{
			ID:      time.Unix(0, int64(index)).Format(time.RFC3339Nano),
			GraphID: "graph",
			RunID:   "run",
			Type:    runtime.EventLLMContentChunk,
			Payload: []byte(`{"call_id":"call","text":"` + string(make([]byte, 512)) + `"}`),
		})
	}
	metrics := hub.Metrics()
	if metrics.CurrentHistoryBytes > 4<<10 || metrics.CurrentHistoryEvents >= 100 {
		t.Fatalf("chunk history is not bounded: %#v", metrics)
	}
}

func TestEventHubSupportsConcurrentPublishAndSubscribe(t *testing.T) {
	hub := newTestEventHub(eventHubOptions{
		subscriberBuffer:  128,
		eventHistoryLimit: 128,
		maxReplay:         128,
	})
	subscription := hub.Subscribe(eventFilter{GraphID: "graph"}, "")
	defer subscription.Unsubscribe()

	var waitGroup sync.WaitGroup
	for index := 0; index < 64; index++ {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			_ = hub.Publish(context.Background(), runtime.Event{
				ID:      time.Unix(0, int64(index)).Format(time.RFC3339Nano),
				GraphID: "graph",
				RunID:   "run",
				Type:    runtime.EventNodeStarted,
			})
		}(index)
	}
	waitGroup.Wait()
	for index := 0; index < 64; index++ {
		select {
		case <-subscription.Events:
		case <-time.After(time.Second):
			t.Fatalf("timed out after %d concurrent events", index)
		}
	}
	if metrics := hub.Metrics(); metrics.PublishedEvents != 64 {
		t.Fatalf("published events = %d, want 64", metrics.PublishedEvents)
	}
}

func newTestEventHub(overrides eventHubOptions) *EventHub {
	options := eventHubOptions{
		subscriberBuffer:   8,
		eventHistoryLimit:  8,
		eventHistoryBytes:  64 << 10,
		streamHistoryLimit: 8,
		streamHistoryBytes: 64 << 10,
		streamHistoryTTL:   time.Minute,
		maxReplay:          8,
		maxPartitions:      8,
		now:                time.Now,
		logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if overrides.subscriberBuffer > 0 {
		options.subscriberBuffer = overrides.subscriberBuffer
	}
	if overrides.eventHistoryLimit > 0 {
		options.eventHistoryLimit = overrides.eventHistoryLimit
	}
	if overrides.eventHistoryBytes > 0 {
		options.eventHistoryBytes = overrides.eventHistoryBytes
	}
	if overrides.streamHistoryLimit > 0 {
		options.streamHistoryLimit = overrides.streamHistoryLimit
	}
	if overrides.streamHistoryBytes > 0 {
		options.streamHistoryBytes = overrides.streamHistoryBytes
	}
	if overrides.streamHistoryTTL > 0 {
		options.streamHistoryTTL = overrides.streamHistoryTTL
	}
	if overrides.maxReplay > 0 {
		options.maxReplay = overrides.maxReplay
	}
	if overrides.maxPartitions > 0 {
		options.maxPartitions = overrides.maxPartitions
	}
	if overrides.now != nil {
		options.now = overrides.now
	}
	return newEventHub(options)
}

func publishTestEvent(t *testing.T, hub *EventHub, event runtime.Event) {
	t.Helper()
	if err := hub.Publish(context.Background(), event); err != nil {
		t.Fatal(err)
	}
}

func jsonPayloadBool(payload []byte, field string) bool {
	var value map[string]any
	if err := json.Unmarshal(payload, &value); err != nil {
		return false
	}
	result, _ := value[field].(bool)
	return result
}
