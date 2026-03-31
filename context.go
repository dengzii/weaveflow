package falcon

import (
	"context"
	"falcon/runtime"
	"fmt"
	"strings"
)

func WithConsoleEvents(ctx context.Context) context.Context {
	return runtime.WithRunnerEventPublisher(ctx, func(eventType runtime.EventType, payload any) error {
		switch eventType {
		case runtime.EventLLMReasoningChunk, runtime.EventLLMContentChunk:
			if text := eventPayloadText(payload); text != "" {
				fmt.Print(text)
			}
		case runtime.EventToolCalled:
			name := eventPayloadString(payload, "name")
			if name != "" {
				fmt.Printf("\n[tool] call %s\n", name)
			}
		case runtime.EventToolReturned:
			name := eventPayloadString(payload, "name")
			content := strings.TrimSpace(eventPayloadString(payload, "content"))
			if name != "" {
				fmt.Printf("[tool] result %s: %s\n\n", name, content)
			}
		case runtime.EventToolFailed:
			name := eventPayloadString(payload, "name")
			message := strings.TrimSpace(eventPayloadString(payload, "error"))
			if name != "" {
				fmt.Printf("[tool] error %s: %s\n\n", name, message)
			}
		}
		return nil
	})
}

func eventPayloadText(payload any) string {
	if message := strings.TrimSpace(eventPayloadString(payload, "text")); message != "" {
		return message
	}
	return ""
}

func eventPayloadString(payload any, key string) string {
	values, ok := payload.(map[string]any)
	if !ok {
		return ""
	}
	text, _ := values[key].(string)
	return text
}
