package neo

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	fruntime "weaveflow/runtime"
)

// LiveMsg is a discriminated union sent over the WebSocket connection.
type LiveMsg struct {
	Type       string          `json:"type"` // snapshot | item | item.update | done | idle
	RunID      string          `json:"run_id,omitempty"`
	SourceName string          `json:"source_name,omitempty"` // snapshot only
	GraphRef   string          `json:"graph_ref,omitempty"`   // snapshot only
	StartedAt  *time.Time      `json:"started_at,omitempty"`  // snapshot only
	Graph      json.RawMessage `json:"graph,omitempty"`       // snapshot only
	Items      []ReplayItem    `json:"items,omitempty"`       // snapshot only
	Item       *ReplayItem     `json:"item,omitempty"`        // item | item.update
	ItemIdx    int             `json:"item_idx,omitempty"`    // item.update only
}

type LiveState struct {
	Running    bool            `json:"running"`
	RunID      string          `json:"run_id,omitempty"`
	SourceName string          `json:"source_name,omitempty"`
	GraphRef   string          `json:"graph_ref,omitempty"`
	StartedAt  *time.Time      `json:"started_at,omitempty"`
	Graph      json.RawMessage `json:"graph,omitempty"`
	Items      []ReplayItem    `json:"items,omitempty"`
}

// LiveHub broadcasts live run events to WebSocket subscribers.
// It implements fruntime.EventSink so it can be inserted into the event pipeline.
type LiveHub struct {
	mu            sync.Mutex
	runID         string
	sourceName    string
	graphRef      string
	startedAt     time.Time
	graph         json.RawMessage
	items         []ReplayItem
	chunkAccumIdx map[string]int // "nodeID:baseType" → index in items; reduces chunk memory
	subs          map[int]chan LiveMsg
	subSeq        int
	running       bool
}

func NewLiveHub() *LiveHub {
	return &LiveHub{
		subs:          make(map[int]chan LiveMsg),
		chunkAccumIdx: make(map[string]int),
	}
}

// StartRun resets hub state for a new run.
func (h *LiveHub) StartRun(graph json.RawMessage, sourceName, graphRef string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.runID = ""
	h.sourceName = sourceName
	h.graphRef = graphRef
	h.startedAt = time.Now()
	h.graph = graph
	h.items = nil
	h.chunkAccumIdx = make(map[string]int)
	h.running = true

	startedAt := h.startedAt
	msg := LiveMsg{
		Type:       "snapshot",
		SourceName: h.sourceName,
		GraphRef:   h.graphRef,
		StartedAt:  &startedAt,
		Graph:      h.graph,
		Items:      nil,
	}
	h.broadcastLocked(msg)
}

// Done signals the run has finished and notifies current subscribers.
func (h *LiveHub) Done() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.running = false
	msg := LiveMsg{Type: "done", RunID: h.runID}
	for _, ch := range h.subs {
		select {
		case ch <- msg:
		default:
		}
	}
}

// Publish implements fruntime.EventSink. Translates the event to a ReplayItem and fans out.
// Streaming chunk events (llm.content_chunk, llm.reasoning_chunk) are accumulated into a
// single item per node stream to prevent unbounded memory growth.
func (h *LiveHub) Publish(_ context.Context, event fruntime.Event) error {
	ev := liveEventToView(event)

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.runID == "" && event.RunID != "" {
		h.runID = event.RunID
	}

	// Accumulate chunk events to avoid storing hundreds of tiny chunk items.
	if baseType, isChunk := chunkBaseType(string(event.Type)); isChunk && event.NodeID != "" {
		chunkText := chunkTextPayload(ev.Payload)
		key := event.NodeID + ":" + baseType

		if idx, exists := h.chunkAccumIdx[key]; exists && idx < len(h.items) {
			// Append text to existing accumulated item and broadcast an update.
			accItem := &h.items[idx]
			if payload, ok := accItem.Event.Payload.(map[string]interface{}); ok {
				if prev, ok2 := payload["text"].(string); ok2 {
					payload["text"] = prev + chunkText
				} else {
					payload["text"] = chunkText
				}
			}
			accItem.Timestamp = ev.Timestamp
			itemCopy := *accItem
			h.broadcastLocked(LiveMsg{Type: "item.update", ItemIdx: idx, Item: &itemCopy})
			return nil
		}

		// First chunk for this stream: create a new accumulated item with the base type.
		accEv := EventView{
			ID:        ev.ID,
			RunID:     ev.RunID,
			StepID:    ev.StepID,
			NodeID:    ev.NodeID,
			Type:      fruntime.EventType(baseType),
			Timestamp: ev.Timestamp,
			Payload:   map[string]interface{}{"text": chunkText},
		}
		item := liveReplayItem(accEv, len(h.items))
		h.items = append(h.items, item)
		h.chunkAccumIdx[key] = len(h.items) - 1
		itemCopy := h.items[len(h.items)-1]
		h.broadcastLocked(LiveMsg{Type: "item", Item: &itemCopy})
		return nil
	}

	item := liveReplayItem(ev, len(h.items))
	h.items = append(h.items, item)
	itemCopy := h.items[len(h.items)-1]
	h.broadcastLocked(LiveMsg{Type: "item", Item: &itemCopy})
	return nil
}

// PublishBatch implements fruntime.EventSink.
func (h *LiveHub) PublishBatch(ctx context.Context, events []fruntime.Event) error {
	for _, event := range events {
		_ = h.Publish(ctx, event)
	}
	return nil
}

// Subscribe returns a channel of live messages and an unsubscribe cleanup func.
// If a run is active, the first message is a full snapshot; otherwise it's an idle sentinel.
func (h *LiveHub) Subscribe() (<-chan LiveMsg, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()

	ch := make(chan LiveMsg, 256)

	id := h.subSeq
	h.subSeq++
	h.subs[id] = ch

	if h.running {
		items := make([]ReplayItem, len(h.items))
		copy(items, h.items)
		startedAt := h.startedAt
		ch <- LiveMsg{
			Type:       "snapshot",
			RunID:      h.runID,
			SourceName: h.sourceName,
			GraphRef:   h.graphRef,
			StartedAt:  &startedAt,
			Graph:      h.graph,
			Items:      items,
		}
	} else {
		ch <- LiveMsg{Type: "idle"}
	}

	return ch, func() {
		h.mu.Lock()
		if sub, ok := h.subs[id]; ok {
			delete(h.subs, id)
			close(sub)
		}
		h.mu.Unlock()
	}
}

// IsRunning reports whether a run is currently in progress.
func (h *LiveHub) IsRunning() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.running
}

func (h *LiveHub) Snapshot() LiveState {
	h.mu.Lock()
	defer h.mu.Unlock()

	snapshot := LiveState{
		Running:    h.running,
		RunID:      h.runID,
		SourceName: h.sourceName,
		GraphRef:   h.graphRef,
	}
	if !h.startedAt.IsZero() {
		startedAt := h.startedAt
		snapshot.StartedAt = &startedAt
	}
	if len(h.graph) > 0 {
		snapshot.Graph = append(json.RawMessage(nil), h.graph...)
	}
	if h.running && len(h.items) > 0 {
		snapshot.Items = make([]ReplayItem, len(h.items))
		copy(snapshot.Items, h.items)
	}
	return snapshot
}

func (h *LiveHub) broadcastLocked(msg LiveMsg) {
	for id, ch := range h.subs {
		select {
		case ch <- msg:
		default:
			close(ch)
			delete(h.subs, id)
		}
	}
}

// chunkBaseType maps streaming chunk event types to their accumulated base type.
// Returns ("", false) for non-chunk events.
func chunkBaseType(eventType string) (string, bool) {
	switch {
	case strings.HasSuffix(eventType, "_chunk"):
		return strings.TrimSuffix(eventType, "_chunk"), true
	}
	return "", false
}

func chunkTextPayload(payload any) string {
	if m, ok := payload.(map[string]interface{}); ok {
		if t, ok2 := m["text"].(string); ok2 {
			return t
		}
	}
	return ""
}

func liveEventToView(event fruntime.Event) EventView {
	var payload any
	if len(event.Payload) > 0 {
		_ = json.Unmarshal(event.Payload, &payload)
	}
	return EventView{
		ID:        event.ID,
		RunID:     event.RunID,
		StepID:    event.StepID,
		NodeID:    event.NodeID,
		Type:      event.Type,
		Timestamp: event.Timestamp,
		Payload:   payload,
	}
}

func liveReplayItem(ev EventView, index int) ReplayItem {
	return ReplayItem{
		Index:     index,
		Timestamp: ev.Timestamp,
		Level:     replayLevel(ev),
		Title:     replayTitle(ev),
		Subtitle:  replaySubtitle(ev),
		Event:     ev,
	}
}
