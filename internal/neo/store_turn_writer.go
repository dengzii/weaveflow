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
	if parts := partsFromEvent(event); len(parts) > 0 {
		w.parts = append(w.parts, parts...)
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

func (w *TurnWriter) AppendAssistantText(text string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	text = strings.TrimSpace(text)
	if text == "" || turnWriterHasText(w.parts, text) {
		return nil
	}
	w.parts = append(w.parts, MessagePart{Type: "text", Text: text})
	return w.store.updateTurnMessage(w.assistantSeq, w.parts, w.status)
}

func partsFromEvent(event fruntime.Event) []MessagePart {
	switch event.Type {
	case fruntime.EventNodeStarted:
		translated := translateNodeStarted(event)
		if translated == nil || strings.TrimSpace(translated.Content) == "" {
			return nil
		}
		return []MessagePart{{Type: "step", Text: translated.Content}}
	case fruntime.EventLLMReasoning:
		text := extractEventPayloadString(event.Payload, "text")
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []MessagePart{{Type: "thinking", Text: text}}
	case fruntime.EventLLMContent:
		if !hasPrefix(event.NodeID, streamableContentPrefixes) {
			return nil
		}
		text := extractEventPayloadString(event.Payload, "text")
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []MessagePart{{Type: "text", Text: text}}
	case fruntime.EventToolCalled:
		return toolCallPartsFromEvent(event)
	case fruntime.EventToolReturned:
		return toolResultPartsFromEvent(event)
	case fruntime.EventToolFailed:
		return toolResultPartsFromEvent(event)
	default:
		return nil
	}
}

func toolCallPartsFromEvent(event fruntime.Event) []MessagePart {
	items := toolPayloadItems(event.Payload)
	if len(items) == 0 {
		return nil
	}

	parts := make([]MessagePart, 0, len(items))
	for _, item := range items {
		parts = append(parts, MessagePart{
			Type: "tool_call",
			ID:   item.ToolCallID,
			Name: item.Name,
			Text: item.Arguments,
		})
	}
	return parts
}

func toolResultPartsFromEvent(event fruntime.Event) []MessagePart {
	items := toolPayloadItems(event.Payload)
	if len(items) == 0 {
		return nil
	}

	parts := make([]MessagePart, 0, len(items))
	for _, item := range items {
		result := item.Result
		if item.failed() {
			result = item.Error
		}
		parts = append(parts, MessagePart{
			Type:   "tool_result",
			ID:     item.ToolCallID,
			Name:   item.Name,
			Result: result,
		})
	}
	return parts
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

func turnWriterHasText(parts []MessagePart, text string) bool {
	for _, part := range parts {
		if part.Type == "text" && strings.TrimSpace(part.Text) == text {
			return true
		}
	}
	return false
}
