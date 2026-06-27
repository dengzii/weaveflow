package server

import (
	"context"
	"sync"

	"github.com/dengzii/weaveflow/runtime"
)

const defaultEventBuffer = 256

type EventHub struct {
	mu          sync.RWMutex
	subscribers map[int]eventSubscriber
	nextID      int
	buffer      int
}

type eventSubscriber struct {
	ch     chan runtime.Event
	filter eventFilter
}

type eventFilter struct {
	RunID  string
	NodeID string
	Types  map[runtime.EventType]struct{}
}

func NewEventHub(buffer int) *EventHub {
	if buffer <= 0 {
		buffer = defaultEventBuffer
	}
	return &EventHub{
		subscribers: map[int]eventSubscriber{},
		buffer:      buffer,
	}
}

func (h *EventHub) Publish(_ context.Context, event runtime.Event) error {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, sub := range h.subscribers {
		if !sub.filter.Match(event) {
			continue
		}
		select {
		case sub.ch <- event:
		default:
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

func (h *EventHub) Subscribe(filter eventFilter) (<-chan runtime.Event, func()) {
	if h == nil {
		ch := make(chan runtime.Event)
		close(ch)
		return ch, func() {}
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	id := h.nextID
	h.nextID++
	ch := make(chan runtime.Event, h.buffer)
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

func (f eventFilter) Match(event runtime.Event) bool {
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
