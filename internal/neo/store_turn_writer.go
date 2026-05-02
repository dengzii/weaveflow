package neo

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	fruntime "weaveflow/runtime"
)

type TurnWriter struct {
	store        *Store
	sessionID    string
	turnID       string
	assistantSeq int64

	mu     sync.Mutex
	parts  []MessagePart
	status string
}

func (w *TurnWriter) Publish(_ context.Context, event fruntime.Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	changed := false
	if part := partFromEvent(event); part != nil {
		w.parts = append(w.parts, *part)
		changed = true
	}

	switch event.Type {
	case fruntime.EventRunFinished:
		w.status = "completed"
		changed = true
	case fruntime.EventRunFailed:
		w.status = "failed"
		changed = true
	case fruntime.EventRunCanceled:
		w.status = "stopped"
		changed = true
	}

	if !changed {
		return nil
	}
	return w.store.updateTurnMessage(w.assistantSeq, w.parts, w.status)
}

func (w *TurnWriter) PublishBatch(ctx context.Context, events []fruntime.Event) error {
	for _, event := range events {
		if err := w.Publish(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (w *TurnWriter) Finalize(status string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if normalized := strings.TrimSpace(status); normalized != "" {
		w.status = normalized
	}
	return w.store.updateTurnMessage(w.assistantSeq, w.parts, w.status)
}

func partFromEvent(event fruntime.Event) *MessagePart {
	switch event.Type {
	case fruntime.EventNodeStarted:
		translated := translateNodeStarted(event)
		if translated == nil || strings.TrimSpace(translated.Content) == "" {
			return nil
		}
		return &MessagePart{Type: "step", Text: translated.Content}
	case fruntime.EventLLMReasoning:
		text := extractEventPayloadString(event.Payload, "text")
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return &MessagePart{Type: "thinking", Text: text}
	case fruntime.EventLLMContent:
		text := extractEventPayloadString(event.Payload, "text")
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return &MessagePart{Type: "text", Text: text}
	case fruntime.EventToolCalled:
		return &MessagePart{
			Type: "tool_call",
			ID:   extractEventPayloadString(event.Payload, "tool_call_id"),
			Name: extractEventPayloadString(event.Payload, "name"),
			Text: extractEventPayloadString(event.Payload, "arguments"),
		}
	case fruntime.EventToolReturned:
		return &MessagePart{
			Type:   "tool_result",
			ID:     extractEventPayloadString(event.Payload, "tool_call_id"),
			Name:   extractEventPayloadString(event.Payload, "name"),
			Result: extractEventPayloadString(event.Payload, "content"),
		}
	case fruntime.EventToolFailed:
		return &MessagePart{
			Type:   "tool_result",
			ID:     extractEventPayloadString(event.Payload, "tool_call_id"),
			Name:   extractEventPayloadString(event.Payload, "name"),
			Result: extractEventPayloadString(event.Payload, "error"),
		}
	default:
		return nil
	}
}

func extractEventPayloadString(payload json.RawMessage, key string) string {
	if len(payload) == 0 {
		return ""
	}

	if key != "" {
		var mapped map[string]any
		if err := json.Unmarshal(payload, &mapped); err == nil {
			if value, ok := mapped[key].(string); ok {
				return value
			}
		}
	}

	var plain string
	if err := json.Unmarshal(payload, &plain); err == nil {
		return plain
	}
	return ""
}
