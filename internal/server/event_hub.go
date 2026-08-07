package server

import (
	"context"
	"sync"

	"github.com/dengzii/weaveflow/runtime"
)

const defaultEventBuffer = 256

type EventHub struct {
	mu           sync.Mutex
	subscribers  map[int]eventSubscriber
	nextID       int
	buffer       int
	historyLimit int
	history      []runtime.Event
}

type eventSubscriber struct {
	ch     chan runtime.Event
	filter eventFilter
}

type eventFilter struct {
	GraphID        string
	GraphSessionID string
	RunID          string
	NodeID         string
	Types          map[runtime.EventType]struct{}
}

func NewEventHub(buffer int) *EventHub {
	if buffer <= 0 {
		buffer = defaultEventBuffer
	}
	historyLimit := buffer * 8
	if historyLimit < 1024 {
		historyLimit = 1024
	}
	return &EventHub{
		subscribers:  map[int]eventSubscriber{},
		buffer:       buffer,
		historyLimit: historyLimit,
	}
}

func (h *EventHub) Publish(_ context.Context, event runtime.Event) error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.history = append(h.history, event)
	if overflow := len(h.history) - h.historyLimit; overflow > 0 {
		copy(h.history, h.history[overflow:])
		h.history = h.history[:h.historyLimit]
	}
	for id, sub := range h.subscribers {
		if !sub.filter.Match(event) {
			continue
		}
		select {
		case sub.ch <- event:
		default:
			delete(h.subscribers, id)
			close(sub.ch)
		}
	}
	return nil
}

func (h *EventHub) PublishBatch(ctx context.Context, events []runtime.Event) error {
	for _, event := range events {
		if err := h.Publish(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (h *EventHub) Subscribe(filter eventFilter, eventCursor ...string) (<-chan runtime.Event, func()) {
	if h == nil {
		ch := make(chan runtime.Event)
		close(ch)
		return ch, func() {}
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	cursor := ""
	if len(eventCursor) > 0 {
		cursor = eventCursor[0]
	}
	replay := h.eventsAfterLocked(filter, cursor)
	id := h.nextID
	h.nextID++
	capacity := h.buffer
	if len(replay) > capacity {
		capacity = len(replay)
	}
	ch := make(chan runtime.Event, capacity)
	for _, event := range replay {
		ch <- event
	}
	h.subscribers[id] = eventSubscriber{ch: ch, filter: filter}

	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if sub, ok := h.subscribers[id]; ok {
			delete(h.subscribers, id)
			close(sub.ch)
		}
	}
}

func (h *EventHub) eventsAfterLocked(filter eventFilter, cursor string) []runtime.Event {
	if cursor == "" {
		return nil
	}
	start := 0
	for index := len(h.history) - 1; index >= 0; index-- {
		if h.history[index].ID == cursor {
			start = index + 1
			break
		}
	}
	result := make([]runtime.Event, 0, len(h.history)-start)
	for _, event := range h.history[start:] {
		if filter.Match(event) {
			result = append(result, event)
		}
	}
	return result
}

func (f eventFilter) Match(event runtime.Event) bool {
	if f.GraphID != "" && event.GraphID != f.GraphID {
		return false
	}
	if f.GraphSessionID != "" && event.GraphSessionID != f.GraphSessionID {
		return false
	}
	if f.RunID != "" && event.RunID != f.RunID {
		return false
	}
	if f.NodeID != "" && event.NodeID != f.NodeID {
		return false
	}
	if len(f.Types) > 0 {
		if _, ok := f.Types[event.Type]; !ok {
			return false
		}
	}
	return true
}
