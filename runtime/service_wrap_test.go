package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dengzii/weaveflow/core"

	"github.com/tmc/langchaingo/llms"
)

type captureEventSink struct {
	events []Event
}

func (s *captureEventSink) Publish(_ context.Context, event Event) error {
	s.events = append(s.events, event)
	return nil
}

func (s *captureEventSink) PublishBatch(ctx context.Context, events []Event) error {
	for _, event := range events {
		if err := s.Publish(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func TestWithRunnerEventServicesWrapsModelAndTools(t *testing.T) {
	t.Parallel()

	sink := &captureEventSink{}
	runner := &GraphRunner{eventSink: sink}
	var llmEvents []EventType
	ctx := WithRunnerEventPublisher(context.Background(), func(eventType EventType, _ any) error {
		llmEvents = append(llmEvents, eventType)
		return nil
	})
	ctx = core.WithModel(ctx, &testLLM{
		response: &llms.ContentResponse{
			Choices: []*llms.ContentChoice{
				{Content: "answer"},
			},
		},
	})
	ctx = core.WithTools(ctx, map[string]core.Tool{
		"calc": {
			Function: &llms.FunctionDefinition{Name: "calculator"},
			Handler: func(_ context.Context, input string) (string, error) {
				if input != "1+2" {
					t.Fatalf("tool input = %q, want %q", input, "1+2")
				}
				return "3", nil
			},
		},
	})

	wrappedCtx := withRunnerEventContext(ctx, runner, "run-1", "step-1", "node-1")
	if wrappedCtx.Model() == nil {
		t.Fatal("wrapped model is nil")
	}
	if _, ok := wrappedCtx.Model().(*llmWrap); !ok {
		t.Fatalf("wrapped model type = %T, want *llmWrap", wrappedCtx.Model())
	}

	if _, err := wrappedCtx.Model().GenerateContent(wrappedCtx, []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "hello"),
	}); err != nil {
		t.Fatalf("GenerateContent() error = %v", err)
	}
	if len(llmEvents) != 2 || llmEvents[0] != EventLLMContent || llmEvents[1] != EventLLMCall {
		t.Fatalf("llm events = %#v, want [%q %q]", llmEvents, EventLLMContent, EventLLMCall)
	}

	tool, ok := core.FindTool(wrappedCtx.Tools(), "calculator")
	if !ok {
		t.Fatal("wrapped tool not found")
	}
	callCtx := core.WithToolCallMetadata(wrappedCtx, core.ToolCallMetadata{
		ToolCallID: "call-1",
		Name:       "calculator",
		Arguments:  `{"expression":"1+2"}`,
	})
	result, err := tool.Handler(callCtx, core.DecodeToolInput(`{"expression":"1+2"}`))
	if err != nil {
		t.Fatalf("tool handler error = %v", err)
	}
	if result != "3" {
		t.Fatalf("tool result = %q, want %q", result, "3")
	}

	if len(sink.events) != 2 {
		t.Fatalf("tool events len = %d, want 2", len(sink.events))
	}
	if sink.events[0].Type != EventToolCalled || sink.events[1].Type != EventToolReturned {
		t.Fatalf("tool event types = [%q %q], want [%q %q]", sink.events[0].Type, sink.events[1].Type, EventToolCalled, EventToolReturned)
	}
	for _, event := range sink.events {
		if event.RunID != "run-1" || event.StepID != "step-1" || event.NodeID != "node-1" {
			t.Fatalf("event identity = %s/%s/%s, want run-1/step-1/node-1", event.RunID, event.StepID, event.NodeID)
		}
	}

	var called map[string]string
	if err := json.Unmarshal(sink.events[0].Payload, &called); err != nil {
		t.Fatalf("unmarshal called payload: %v", err)
	}
	if called["tool_call_id"] != "call-1" || called["name"] != "calculator" || called["arguments"] != `{"expression":"1+2"}` {
		t.Fatalf("called payload = %#v", called)
	}
}
