package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/llms"
)

type captureEventSink struct {
	events []Event
}

func (sink *captureEventSink) Publish(_ context.Context, event Event) error {
	sink.events = append(sink.events, event)
	return nil
}

func (sink *captureEventSink) PublishBatch(ctx context.Context, events []Event) error {
	for _, event := range events {
		if err := sink.Publish(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func TestWithRunnerEventContextObservesModelAndTools(t *testing.T) {
	t.Parallel()

	sink := &captureEventSink{}
	runner := &GraphRunner{eventSink: sink}
	var modelEvents []EventType
	ctx := WithRunnerEventPublisher(context.Background(), func(eventType EventType, _ any) error {
		modelEvents = append(modelEvents, eventType)
		return nil
	})
	model := &testLLM{response: &llms.ModelResponse{Choices: []*llms.ModelChoice{{Content: "answer"}}}}
	ctx = core.WithModel(ctx, model)
	ctx = core.WithTools(ctx, map[string]core.Tool{
		"calc": {
			Function: &llms.FunctionDefinition{Name: "calculator"},
			Handler: func(_ context.Context, call llms.ToolCall) (llms.ToolResult, error) {
				if call.FunctionCall == nil || string(call.FunctionCall.Arguments) != `{"expression":"1+2"}` {
					t.Fatalf("tool call = %#v", call)
				}
				return llms.ToolResult{Content: "3", Value: 3}, nil
			},
		},
	})

	observedCtx := withRunnerEventContext(ctx, runner, "run-1", "step-1", "node-1")
	if observedCtx.Model() != model {
		t.Fatalf("observed model = %T, want original model", observedCtx.Model())
	}
	if _, err := core.GenerateModel(observedCtx, observedCtx.Model(), llms.ModelRequest{
		Mode:     llms.ModelModeChat,
		Messages: []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "hello")},
	}); err != nil {
		t.Fatalf("GenerateModel() error = %v", err)
	}
	if len(modelEvents) != 2 || modelEvents[0] != EventLLMContent || modelEvents[1] != EventLLMCall {
		t.Fatalf("model events = %#v", modelEvents)
	}

	tool, ok := core.FindTool(observedCtx.Tools(), "calculator")
	if !ok {
		t.Fatal("tool not found")
	}
	result, err := core.ExecuteTool(observedCtx, tool, llms.ToolCall{
		ID:   "call-1",
		Type: "function",
		FunctionCall: &llms.FunctionCall{
			Name:      "calculator",
			Arguments: json.RawMessage(`{"expression":"1+2"}`),
		},
	})
	if err != nil {
		t.Fatalf("ExecuteTool() error = %v", err)
	}
	if result.Content != "3" {
		t.Fatalf("tool result = %#v", result)
	}

	if len(sink.events) != 3 {
		t.Fatalf("tool events len = %d, want 3", len(sink.events))
	}
	wantTypes := []EventType{EventToolCalled, EventToolStarted, EventToolReturned}
	for index, event := range sink.events {
		if event.Type != wantTypes[index] {
			t.Fatalf("event %d type = %q, want %q", index, event.Type, wantTypes[index])
		}
		if event.RunID != "run-1" || event.StepID != "step-1" || event.NodeID != "node-1" {
			t.Fatalf("event identity = %s/%s/%s", event.RunID, event.StepID, event.NodeID)
		}
	}
	var called struct {
		ToolCallID string          `json:"tool_call_id"`
		Name       string          `json:"name"`
		Arguments  json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(sink.events[0].Payload, &called); err != nil {
		t.Fatalf("unmarshal called payload: %v", err)
	}
	if called.ToolCallID != "call-1" || called.Name != "calculator" || string(called.Arguments) != `{"expression":"1+2"}` {
		t.Fatalf("called payload = %#v", called)
	}
}
