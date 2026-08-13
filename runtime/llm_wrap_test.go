package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/llms"
)

type testLLM struct {
	response *llms.ModelResponse
	err      error
}

func (model *testLLM) Generate(context.Context, llms.ModelRequest) (*llms.ModelResponse, error) {
	return model.response, model.err
}

type streamingTestLLM struct {
	chunks []string
}

func (model *streamingTestLLM) Generate(ctx context.Context, request llms.ModelRequest) (*llms.ModelResponse, error) {
	for _, chunk := range model.chunks {
		if request.Stream != nil {
			if err := request.Stream(ctx, llms.ModelStreamEvent{Type: llms.ModelStreamContent, Text: chunk}); err != nil {
				return nil, err
			}
		}
	}
	return &llms.ModelResponse{Choices: []*llms.ModelChoice{{Content: "hello world"}}}, nil
}

func TestModelObserverPublishesFinalReasoningAndContentEvents(t *testing.T) {
	t.Parallel()

	model := &testLLM{response: &llms.ModelResponse{Choices: []*llms.ModelChoice{{
		ReasoningContent: "reasoning text",
		Content:          "final answer",
	}}}}
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
	ctx = core.WithModelCallObserver(ctx, modelCallEventObserver(&GraphRunner{}, "run-1", "step-1", "node-1"))

	_, err := core.GenerateModel(ctx, model, llms.ModelRequest{
		Mode:     llms.ModelModeChat,
		Messages: []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "hello")},
	})
	if err != nil {
		t.Fatalf("GenerateModel() error = %v", err)
	}

	if len(events) != 3 {
		t.Fatalf("published events len = %d, want 3", len(events))
	}
	if events[0].typ != EventLLMReasoning || events[1].typ != EventLLMContent || events[2].typ != EventLLMCall {
		t.Fatalf("event types = [%q %q %q]", events[0].typ, events[1].typ, events[2].typ)
	}
	var reasoningPayload map[string]string
	if err := json.Unmarshal(events[0].payload, &reasoningPayload); err != nil {
		t.Fatalf("unmarshal reasoning payload: %v", err)
	}
	if reasoningPayload["text"] != "reasoning text" {
		t.Fatalf("reasoning payload = %#v", reasoningPayload)
	}
	var contentPayload map[string]string
	if err := json.Unmarshal(events[1].payload, &contentPayload); err != nil {
		t.Fatalf("unmarshal content payload: %v", err)
	}
	if contentPayload["text"] != "final answer" {
		t.Fatalf("content payload = %#v", contentPayload)
	}
}

func TestModelObserverPublishesCompletionUsage(t *testing.T) {
	t.Parallel()

	model := &testLLM{response: &llms.ModelResponse{
		Choices: []*llms.ModelChoice{{Content: "completion text"}},
		Usage:   llms.ModelUsage{InputTokens: 4, OutputTokens: 3, TotalTokens: 7},
	}}
	var events []map[string]any
	var eventTypes []EventType
	ctx := WithRunnerEventPublisher(context.Background(), func(eventType EventType, payload any) error {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		decoded := map[string]any{}
		if err := json.Unmarshal(data, &decoded); err != nil {
			return err
		}
		eventTypes = append(eventTypes, eventType)
		events = append(events, decoded)
		return nil
	})
	ctx = core.WithModelCallObserver(ctx, modelCallEventObserver(&GraphRunner{}, "run-1", "step-1", "node-1"))
	response, err := core.GenerateModel(ctx, model, llms.ModelRequest{Mode: llms.ModelModeCompletion, Prompt: "prompt"})
	if err != nil {
		t.Fatalf("GenerateModel() error = %v", err)
	}
	if response.Choices[0].Content != "completion text" {
		t.Fatalf("response = %#v", response)
	}
	if len(eventTypes) != 2 || eventTypes[0] != EventLLMContent || eventTypes[1] != EventLLMCall {
		t.Fatalf("event types = %#v", eventTypes)
	}
	if events[1]["prompt_tokens"] != float64(4) || events[1]["completion_tokens"] != float64(3) || events[1]["total_tokens"] != float64(7) {
		t.Fatalf("usage event = %#v", events[1])
	}
}

func TestModelObserverContentChunksCarryStableCallIDAndPreserveWhitespace(t *testing.T) {
	t.Parallel()

	model := &streamingTestLLM{chunks: []string{"hello", " ", "world"}}
	type publishedEvent struct {
		typ     EventType
		payload map[string]any
	}
	var events []publishedEvent
	ctx := WithRunnerEventPublisher(context.Background(), func(eventType EventType, payload any) error {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		decoded := map[string]any{}
		if err := json.Unmarshal(data, &decoded); err != nil {
			return err
		}
		events = append(events, publishedEvent{typ: eventType, payload: decoded})
		return nil
	})
	ctx = core.WithModelCallObserver(ctx, modelCallEventObserver(&GraphRunner{}, "run-1", "step-1", "node-1"))
	if _, err := core.GenerateModel(ctx, model, llms.ModelRequest{
		Mode:     llms.ModelModeChat,
		Messages: []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "hello")},
	}); err != nil {
		t.Fatal(err)
	}
	if len(events) != 5 {
		t.Fatalf("events = %#v", events)
	}
	callID, _ := events[0].payload["call_id"].(string)
	if callID == "" {
		t.Fatal("chunk call_id is empty")
	}
	for index, text := range []string{"hello", " ", "world"} {
		if events[index].typ != EventLLMContentChunk || events[index].payload["text"] != text || events[index].payload["call_id"] != callID {
			t.Fatalf("events[%d] = %#v", index, events[index])
		}
	}
	if events[3].typ != EventLLMContent || events[3].payload["call_id"] != callID {
		t.Fatalf("final content event = %#v", events[3])
	}
	if events[4].typ != EventLLMCall || events[4].payload["call_id"] != callID {
		t.Fatalf("call event = %#v", events[4])
	}
}
