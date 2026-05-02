package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tmc/langchaingo/llms"
)

type testLLM struct {
	response *llms.ContentResponse
	err      error
}

func (t *testLLM) GenerateContent(_ context.Context, _ []llms.MessageContent, _ ...llms.CallOption) (*llms.ContentResponse, error) {
	return t.response, t.err
}

func (t *testLLM) Call(_ context.Context, _ string, _ ...llms.CallOption) (string, error) {
	return "", t.err
}

func TestWrapLLMGenerateContentPublishesFinalReasoningAndContentEvents(t *testing.T) {
	t.Parallel()

	model := WrapLLM(&testLLM{
		response: &llms.ContentResponse{
			Choices: []*llms.ContentChoice{
				{
					ReasoningContent: "reasoning text",
					Content:          "final answer",
				},
			},
		},
	})

	type publishedEvent struct {
		typ     EventType
		payload json.RawMessage
	}
	var events []publishedEvent
	ctx := WithRunnerEventPublisher(context.Background(), func(eventType EventType, payload any) error {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		events = append(events, publishedEvent{typ: eventType, payload: data})
		return nil
	})

	_, err := model.GenerateContent(ctx, []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "hello"),
	})
	if err != nil {
		t.Fatalf("GenerateContent() error = %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("published events len = %d, want 2", len(events))
	}
	if events[0].typ != EventLLMReasoning {
		t.Fatalf("events[0].typ = %q, want %q", events[0].typ, EventLLMReasoning)
	}
	if events[1].typ != EventLLMContent {
		t.Fatalf("events[1].typ = %q, want %q", events[1].typ, EventLLMContent)
	}

	var reasoningPayload map[string]string
	if err := json.Unmarshal(events[0].payload, &reasoningPayload); err != nil {
		t.Fatalf("unmarshal reasoning payload: %v", err)
	}
	if reasoningPayload["text"] != "reasoning text" {
		t.Fatalf("reasoning payload text = %q, want %q", reasoningPayload["text"], "reasoning text")
	}

	var contentPayload map[string]string
	if err := json.Unmarshal(events[1].payload, &contentPayload); err != nil {
		t.Fatalf("unmarshal content payload: %v", err)
	}
	if contentPayload["text"] != "final answer" {
		t.Fatalf("content payload text = %q, want %q", contentPayload["text"], "final answer")
	}
}
