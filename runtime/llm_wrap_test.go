package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dengzii/weaveflow/core"

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

func (t *testLLM) GenerateCompletion(_ context.Context, _ string, _ ...llms.CallOption) (*llms.ContentResponse, error) {
	return t.response, t.err
}

func TestWrapLlmGenerateContentPublishesFinalReasoningAndContentEvents(t *testing.T) {
	t.Parallel()

	model := wrapLlm(&testLLM{
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

	if len(events) != 3 {
		t.Fatalf("published events len = %d, want 3", len(events))
	}
	if events[0].typ != EventLLMReasoning {
		t.Fatalf("events[0].typ = %q, want %q", events[0].typ, EventLLMReasoning)
	}
	if events[1].typ != EventLLMContent {
		t.Fatalf("events[1].typ = %q, want %q", events[1].typ, EventLLMContent)
	}
	if events[2].typ != EventLLMCall {
		t.Fatalf("events[2].typ = %q, want %q", events[2].typ, EventLLMCall)
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

func TestWrapLlmGenerateCompletionPublishesContentAndUsageEvents(t *testing.T) {
	t.Parallel()

	model := wrapLlm(&testLLM{response: &llms.ContentResponse{Choices: []*llms.ContentChoice{{
		Content: "completion text",
		GenerationInfo: map[string]any{
			"PromptTokens":     4,
			"CompletionTokens": 3,
			"TotalTokens":      7,
		},
	}}}})
	completionModel, ok := model.(core.CompletionModel)
	if !ok {
		t.Fatalf("wrapped model type = %T, want core.CompletionModel", model)
	}

	var eventTypes []EventType
	ctx := WithRunnerEventPublisher(context.Background(), func(eventType EventType, _ any) error {
		eventTypes = append(eventTypes, eventType)
		return nil
	})
	response, err := completionModel.GenerateCompletion(ctx, "prompt")
	if err != nil {
		t.Fatalf("GenerateCompletion() error = %v", err)
	}
	if response.Choices[0].Content != "completion text" {
		t.Fatalf("response = %#v", response)
	}
	if len(eventTypes) != 2 || eventTypes[0] != EventLLMContent || eventTypes[1] != EventLLMCall {
		t.Fatalf("event types = %#v", eventTypes)
	}
}
