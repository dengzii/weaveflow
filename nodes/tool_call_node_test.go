package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"weaveflow/core"
	fruntime "weaveflow/runtime"
	wfstate "weaveflow/state"
	"weaveflow/tools"

	"github.com/tmc/langchaingo/llms"
)

type capturedToolEvent struct {
	Type    fruntime.EventType
	Payload json.RawMessage
}

func TestToolsNodePublishesBatchedParallelCallAndIndividualResults(t *testing.T) {
	t.Parallel()

	state := wfstate.NewBaseState([]llms.MessageContent{
		{
			Role: llms.ChatMessageTypeAI,
			Parts: []llms.ContentPart{
				testToolCall("call_1", "alpha", `{"input":"one"}`),
				testToolCall("call_2", "beta", `{"input":"two"}`),
			},
		},
	}, wfstate.DefaultMaxIterations)

	ctx, events := toolTestContext(map[string]tools.Tool{
		"alpha": testTool("alpha", func(_ context.Context, input string) (string, error) {
			return "alpha:" + input, nil
		}),
		"beta": testTool("beta", func(_ context.Context, input string) (string, error) {
			return "beta:" + input, nil
		}),
	})

	node := NewToolCallNode()
	node.Parallel = true
	got, err := node.execute(ctx, state)
	if err != nil {
		t.Fatalf("execute() error = %v", err)
	}

	if len(*events) != 3 {
		t.Fatalf("published events len = %d, want 3: %#v", len(*events), *events)
	}
	if (*events)[0].Type != fruntime.EventToolCalled {
		t.Fatalf("first event type = %q, want %q", (*events)[0].Type, fruntime.EventToolCalled)
	}

	var called struct {
		Tools []struct {
			ToolCallID string `json:"tool_call_id"`
			Name       string `json:"name"`
			Arguments  string `json:"arguments"`
		} `json:"tools"`
		Count    int  `json:"count"`
		Parallel bool `json:"parallel"`
	}
	if err := json.Unmarshal((*events)[0].Payload, &called); err != nil {
		t.Fatalf("unmarshal tool.called payload: %v", err)
	}
	if called.Count != 2 || !called.Parallel || len(called.Tools) != 2 {
		t.Fatalf("tool.called payload = %#v, want two parallel tools", called)
	}
	if called.Tools[0].ToolCallID != "call_1" || called.Tools[1].ToolCallID != "call_2" {
		t.Fatalf("tool.called order = %#v", called.Tools)
	}

	returned := map[string]string{}
	for _, event := range (*events)[1:] {
		if event.Type != fruntime.EventToolReturned {
			t.Fatalf("result event type = %q, want %q", event.Type, fruntime.EventToolReturned)
		}
		var payload struct {
			ToolCallID string `json:"tool_call_id"`
			Name       string `json:"name"`
			Content    string `json:"content"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("unmarshal tool.returned payload: %v", err)
		}
		returned[payload.ToolCallID] = payload.Content
	}
	if returned["call_1"] != "alpha:one" || returned["call_2"] != "beta:two" {
		t.Fatalf("tool.returned content = %#v", returned)
	}

	messages := got.Conversation("").Messages()
	if len(messages) != 3 {
		t.Fatalf("messages len = %d, want 3", len(messages))
	}
	if messages[1].Role != llms.ChatMessageTypeTool || messages[2].Role != llms.ChatMessageTypeTool {
		t.Fatalf("tool messages not appended in order: %#v", messages)
	}
}

func TestToolsNodePublishesIndividualParallelFailureResult(t *testing.T) {
	t.Parallel()

	state := wfstate.NewBaseState([]llms.MessageContent{
		{
			Role: llms.ChatMessageTypeAI,
			Parts: []llms.ContentPart{
				testToolCall("call_1", "alpha", `{"input":"one"}`),
				testToolCall("call_2", "beta", `{"input":"two"}`),
			},
		},
	}, wfstate.DefaultMaxIterations)

	ctx, events := toolTestContext(map[string]tools.Tool{
		"alpha": testTool("alpha", func(_ context.Context, input string) (string, error) {
			return "alpha:" + input, nil
		}),
		"beta": testTool("beta", func(context.Context, string) (string, error) {
			return "", errors.New("boom")
		}),
	})

	node := NewToolCallNode()
	node.Parallel = true
	if _, err := node.execute(ctx, state); err != nil {
		t.Fatalf("execute() error = %v", err)
	}

	if len(*events) != 3 {
		t.Fatalf("published events len = %d, want 3: %#v", len(*events), *events)
	}
	if (*events)[0].Type != fruntime.EventToolCalled {
		t.Fatalf("first event type = %q, want %q", (*events)[0].Type, fruntime.EventToolCalled)
	}

	results := map[string]capturedToolEvent{}
	for _, event := range (*events)[1:] {
		var payload struct {
			ToolCallID string `json:"tool_call_id"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("unmarshal result payload: %v", err)
		}
		results[payload.ToolCallID] = event
	}

	if results["call_1"].Type != fruntime.EventToolReturned {
		t.Fatalf("call_1 event type = %q, want %q", results["call_1"].Type, fruntime.EventToolReturned)
	}
	if results["call_2"].Type != fruntime.EventToolFailed {
		t.Fatalf("call_2 event type = %q, want %q", results["call_2"].Type, fruntime.EventToolFailed)
	}
	var failed struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(results["call_2"].Payload, &failed); err != nil {
		t.Fatalf("unmarshal failed payload: %v", err)
	}
	if failed.Error != "boom" {
		t.Fatalf("failed error = %q, want boom", failed.Error)
	}
}

func testToolCall(id, name, arguments string) llms.ToolCall {
	return llms.ToolCall{
		ID:   id,
		Type: "function",
		FunctionCall: &llms.FunctionCall{
			Name:      name,
			Arguments: arguments,
		},
	}
}

func testTool(name string, handler tools.ToolHandler) tools.Tool {
	return tools.Tool{
		Function: &llms.FunctionDefinition{Name: name},
		Handler:  handler,
	}
}

func toolTestContext(testTools map[string]tools.Tool) (context.Context, *[]capturedToolEvent) {
	var mu sync.Mutex
	events := make([]capturedToolEvent, 0)
	ctx := core.WithServices(context.Background(), &core.Services{Tools: testTools})
	ctx = fruntime.WithRunnerEventPublisher(ctx, func(eventType fruntime.EventType, payload any) error {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		events = append(events, capturedToolEvent{Type: eventType, Payload: data})
		return nil
	})
	return ctx, &events
}
