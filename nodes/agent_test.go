package nodes

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"weaveflow/core"
	wfstate "weaveflow/state"
	"weaveflow/tools"

	"github.com/tmc/langchaingo/llms"
)

// scriptedModel returns a queued sequence of ContentResponses. Each
// GenerateContent call pops the next response. Useful for driving multi-turn
// agent loops in tests without a real LLM.
type scriptedModel struct {
	mu        sync.Mutex
	responses []*llms.ContentResponse
	calls     int
	captured  [][]llms.MessageContent
}

func (m *scriptedModel) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.captured = append(m.captured, cloneReducerMessages(messages))
	if m.calls >= len(m.responses) {
		return nil, errors.New("scriptedModel: no more queued responses")
	}
	resp := m.responses[m.calls]
	m.calls++
	return resp, nil
}

func (m *scriptedModel) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	return "", nil
}

func textResponse(text string) *llms.ContentResponse {
	return &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{Content: text}},
	}
}

func toolCallResponse(callID, name, arguments string) *llms.ContentResponse {
	return &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{
			ToolCalls: []llms.ToolCall{{
				ID:   callID,
				Type: "function",
				FunctionCall: &llms.FunctionCall{
					Name:      name,
					Arguments: arguments,
				},
			}},
		}},
	}
}

func TestAgentNodeTerminatesOnNoToolCalls(t *testing.T) {
	t.Parallel()

	model := &scriptedModel{responses: []*llms.ContentResponse{
		textResponse("the answer is 42"),
	}}
	node := NewAgentNode()
	node.StateScope = "subagent"
	node.SystemPrompt = "you are concise"
	node.InputPath = "task"

	state := wfstate.State{"task": "what is the meaning"}

	ctx := core.WithServices(context.Background(), &core.Services{Model: model})
	next, err := runTestNode(t, node, ctx, state)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if model.calls != 1 {
		t.Fatalf("expected exactly 1 model call, got %d", model.calls)
	}

	conv := next.Conversation("subagent")
	if got := conv.FinalAnswer(); got != "the answer is 42" {
		t.Fatalf("final answer = %q, want %q", got, "the answer is 42")
	}
	if got := conv.IterationCount(); got != 1 {
		t.Fatalf("iteration count = %d, want 1", got)
	}

	msgs := conv.Messages()
	if len(msgs) < 3 {
		t.Fatalf("expected at least system+human+ai messages, got %d: %#v", len(msgs), msgs)
	}
	if msgs[0].Role != llms.ChatMessageTypeSystem || extractText(msgs[0]) != "you are concise" {
		t.Fatalf("system message not seeded: %#v", msgs[0])
	}
	if msgs[1].Role != llms.ChatMessageTypeHuman || extractText(msgs[1]) != "what is the meaning" {
		t.Fatalf("human task not seeded from input_path: %#v", msgs[1])
	}
}

func TestAgentNodeRespectsMaxIterations(t *testing.T) {
	t.Parallel()

	// The model keeps emitting tool calls forever — agent must stop on cap.
	model := &scriptedModel{responses: []*llms.ContentResponse{
		toolCallResponse("c1", "noop", `{"input":"a"}`),
		toolCallResponse("c2", "noop", `{"input":"b"}`),
		toolCallResponse("c3", "noop", `{"input":"c"}`),
	}}
	node := NewAgentNode()
	node.StateScope = "subagent"
	node.SystemPrompt = "loop"
	node.MaxIterations = 2
	node.ToolIDs = []string{"noop"}

	state := wfstate.State{}
	state.Conversation("subagent").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "go"),
	})

	tooling := map[string]tools.Tool{
		"noop": testTool("noop", func(_ context.Context, input string) (string, error) {
			return "ok:" + input, nil
		}),
	}
	ctx := core.WithServices(context.Background(), &core.Services{Model: model, Tools: tooling})

	next, err := runTestNode(t, node, ctx, state)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if model.calls != 2 {
		t.Fatalf("expected the loop to stop after 2 model calls, got %d", model.calls)
	}

	conv := next.Conversation("subagent")
	if got := conv.IterationCount(); got != 2 {
		t.Fatalf("iteration count = %d, want 2", got)
	}
	final := conv.FinalAnswer()
	if !strings.Contains(strings.ToLower(final), "maximum") {
		t.Fatalf("final answer should announce the iteration cap, got %q", final)
	}
}

func TestAgentNodeExecutesToolsAcrossIterations(t *testing.T) {
	t.Parallel()

	model := &scriptedModel{responses: []*llms.ContentResponse{
		toolCallResponse("c1", "echo", `{"input":"hi"}`),
		textResponse("done after tool"),
	}}
	node := NewAgentNode()
	node.StateScope = "subagent"
	node.SystemPrompt = "use the tool first"
	node.InputPath = "task"
	node.OutputPath = "result"
	node.ToolIDs = []string{"echo"}

	state := wfstate.State{"task": "please run"}

	echoCalls := 0
	var muEcho sync.Mutex
	tooling := map[string]tools.Tool{
		"echo": testTool("echo", func(_ context.Context, input string) (string, error) {
			muEcho.Lock()
			echoCalls++
			muEcho.Unlock()
			return "echo:" + input, nil
		}),
	}
	ctx := core.WithServices(context.Background(), &core.Services{Model: model, Tools: tooling})

	next, err := runTestNode(t, node, ctx, state)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if model.calls != 2 {
		t.Fatalf("model calls = %d, want 2", model.calls)
	}
	if echoCalls != 1 {
		t.Fatalf("echo tool called %d times, want 1", echoCalls)
	}

	conv := next.Conversation("subagent")
	if got := conv.FinalAnswer(); got != "done after tool" {
		t.Fatalf("final answer = %q, want %q", got, "done after tool")
	}
	if got, _ := next["result"].(string); got != "done after tool" {
		t.Fatalf("output_path value = %q, want %q", got, "done after tool")
	}

	// Second model call must have seen the tool response in its prompt.
	if len(model.captured) != 2 {
		t.Fatalf("captured turns = %d, want 2", len(model.captured))
	}
	second := model.captured[1]
	sawToolResponse := false
	for _, msg := range second {
		if msg.Role == llms.ChatMessageTypeTool {
			for _, part := range msg.Parts {
				if resp, ok := part.(llms.ToolCallResponse); ok && strings.Contains(resp.Content, "echo:") {
					sawToolResponse = true
				}
			}
		}
	}
	if !sawToolResponse {
		t.Fatalf("second model call did not see the tool response: %#v", second)
	}
}

func TestAgentNodeAsToolRunsIsolatedConversation(t *testing.T) {
	t.Parallel()

	model := &scriptedModel{responses: []*llms.ContentResponse{
		textResponse("delegated answer"),
	}}
	node := NewAgentNode()
	node.StateScope = "subagent"
	node.SystemPrompt = "you handle delegated tasks"
	node.ToolName = "delegate"
	node.ToolDescription = "Delegate a task to the sub-agent."

	tool := node.AsTool()
	if tool.Function == nil || tool.Function.Name != "delegate" {
		t.Fatalf("agent tool definition not set up correctly: %#v", tool.Function)
	}
	if tool.Handler == nil {
		t.Fatalf("agent tool handler is nil")
	}

	ctx := core.WithServices(context.Background(), &core.Services{Model: model})

	result, err := tool.Handler(ctx, `{"task":"please answer"}`)
	if err != nil {
		t.Fatalf("tool handler: %v", err)
	}
	if result != "delegated answer" {
		t.Fatalf("tool result = %q, want %q", result, "delegated answer")
	}
	if model.calls != 1 {
		t.Fatalf("model calls = %d, want 1", model.calls)
	}

	// The tool handler must have built a fresh state — passing the same
	// agent again with no parent state should keep working identically.
	model2 := &scriptedModel{responses: []*llms.ContentResponse{
		textResponse("second answer"),
	}}
	ctx2 := core.WithServices(context.Background(), &core.Services{Model: model2})
	result2, err := tool.Handler(ctx2, `{"task":"another"}`)
	if err != nil {
		t.Fatalf("second tool handler: %v", err)
	}
	if result2 != "second answer" {
		t.Fatalf("second tool result = %q, want %q", result2, "second answer")
	}
}
