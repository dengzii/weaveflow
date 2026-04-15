package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

func WithLLMStreamingResponseEvent() llms.CallOption {
	return func(o *llms.CallOptions) {
		o.StreamingReasoningFunc = emitStreamingResponse
	}
}

func emitStreamingResponse(ctx context.Context, reasoningChunk, chunk []byte) error {
	reasoning := string(reasoningChunk)
	if strings.TrimSpace(reasoning) != "" {
		if err := PublishRunnerContextEvent(ctx, EventLLMReasoningChunk, map[string]any{"text": reasoning}); err != nil {
			return err
		}
		if !HasRunnerEventPublisher(ctx) {
			fmt.Print(reasoning)
		}
	}
	content := string(chunk)
	if strings.TrimSpace(content) != "" {
		if err := PublishRunnerContextEvent(ctx, EventLLMContentChunk, map[string]any{"text": content}); err != nil {
			return err
		}
		if !HasRunnerEventPublisher(ctx) {
			fmt.Print(content)
		}
	}
	return nil
}
