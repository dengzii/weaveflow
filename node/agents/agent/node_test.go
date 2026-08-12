package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	"github.com/dengzii/weaveflow/core"
	basenode "github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/state"

	"github.com/tmc/langchaingo/llms"
)

func TestNodeUsesExplicitTaskConversationAndResultPaths(t *testing.T) {
	t.Parallel()
	taskPath := state.Shared("request")
	conversationPath := state.Scope("researcher", "conversation")
	resultPath := state.Shared("handoff", "research")
	target := NewNode(core.WithID("researcher"))
	target.TaskPath = taskPath
	target.ConversationPath = conversationPath
	target.ResultPath = resultPath
	model := &scriptedModel{responses: []*llms.ContentResponse{{Choices: []*llms.ContentChoice{{Content: "research result"}}}}}
	result, err := core.ExecuteNode(core.WithModel(context.Background(), model), state.FromShared(map[string]any{"request": "research this"}), target)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	value, _ := state.ReadPath(result.State, resultPath.String())
	if value != "research result" {
		t.Fatalf("result = %#v", value)
	}
	view, _ := conversationcap.Bind(state.NewAccess(result.State), conversationPath)
	if len(view.Messages()) != 2 {
		t.Fatalf("conversation = %#v", view.Messages())
	}
}

func TestToolUsesExplicitMetadataAndRunsAgent(t *testing.T) {
	t.Parallel()

	model := &scriptedModel{responses: []*llms.ContentResponse{{Choices: []*llms.ContentChoice{{Content: "research result"}}}}}
	tool, err := NewTool(ToolConfig{
		Name:        "research_agent",
		Description: "Delegate research to a specialist.",
		Agent: Config{
			SystemPrompt: "Return a concise research result.",
		},
	})
	if err != nil {
		t.Fatalf("new agent tool: %v", err)
	}

	if tool.Function.Name != "research_agent" || tool.Function.Description != "Delegate research to a specialist." {
		t.Fatalf("tool function = %#v", tool.Function)
	}
	ctx := core.WithTools(core.WithModel(context.Background(), model), map[string]core.Tool{
		"unconfigured": core.NewTool(&llms.FunctionDefinition{Name: "unconfigured"}, nil),
	})
	result, err := tool.Handler(ctx, `{"task":"research this"}`)
	if err != nil {
		t.Fatalf("execute agent tool: %v", err)
	}
	if result != "research result" {
		t.Fatalf("result = %q, want research result", result)
	}
	if len(model.options) != 1 || len(model.options[0].Tools) != 0 {
		t.Fatalf("injected tools = %#v, want none", model.options[0].Tools)
	}
}

func TestToolRejectsUnsafeConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config ToolConfig
	}{
		{name: "missing name", config: ToolConfig{Description: "Delegate work."}},
		{name: "missing description", config: ToolConfig{Name: "worker"}},
		{name: "empty tool id", config: ToolConfig{Name: "worker", Description: "Delegate work.", Agent: Config{ToolIDs: []string{" "}}}},
		{name: "duplicate tool id", config: ToolConfig{Name: "worker", Description: "Delegate work.", Agent: Config{ToolIDs: []string{"read", "READ"}}}},
		{name: "self reference", config: ToolConfig{Name: "worker", Description: "Delegate work.", Agent: Config{ToolIDs: []string{"worker"}}}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := NewTool(testCase.config); err == nil {
				t.Fatal("expected configuration error")
			}
		})
	}
}

func TestToolRestrictsInternalTools(t *testing.T) {
	t.Parallel()

	model := &scriptedModel{responses: []*llms.ContentResponse{
		{Choices: []*llms.ContentChoice{{ToolCalls: []llms.ToolCall{{
			ID:   "call",
			Type: "function",
			FunctionCall: &llms.FunctionCall{
				Name:      "other",
				Arguments: `{}`,
			},
		}}}}},
		{Choices: []*llms.ContentChoice{{Content: "finished"}}},
	}}
	otherCalled := false
	availableTools := map[string]core.Tool{
		"allowed": core.NewTool(&llms.FunctionDefinition{Name: "allowed"}, func(context.Context, string) (string, error) {
			return "allowed", nil
		}),
		"other": core.NewTool(&llms.FunctionDefinition{Name: "other"}, func(context.Context, string) (string, error) {
			otherCalled = true
			return "other", nil
		}),
	}
	tool, err := NewTool(ToolConfig{
		Name:        "worker",
		Description: "Delegate work.",
		Agent:       Config{ToolIDs: []string{"allowed"}},
	})
	if err != nil {
		t.Fatalf("new agent tool: %v", err)
	}
	ctx := core.WithTools(core.WithModel(context.Background(), model), availableTools)
	result, err := tool.Handler(ctx, `{"task":"do work"}`)
	if err != nil {
		t.Fatalf("execute agent tool: %v", err)
	}
	if result != "finished" {
		t.Fatalf("result = %q, want finished", result)
	}
	if otherCalled {
		t.Fatal("agent tool executed a tool outside its allowlist")
	}
	if len(model.options) == 0 || len(model.options[0].Tools) != 1 || model.options[0].Tools[0].Function.Name != "allowed" {
		t.Fatalf("injected tools = %#v", model.options[0].Tools)
	}
}

func TestToolRequiresConfiguredToolsAtRuntime(t *testing.T) {
	t.Parallel()

	model := &scriptedModel{responses: []*llms.ContentResponse{{Choices: []*llms.ContentChoice{{Content: "unused"}}}}}
	tool, err := NewTool(ToolConfig{
		Name:        "worker",
		Description: "Delegate work.",
		Agent:       Config{ToolIDs: []string{"missing"}},
	})
	if err != nil {
		t.Fatalf("new agent tool: %v", err)
	}
	_, err = tool.Handler(core.WithModel(context.Background(), model), `{"task":"do work"}`)
	if err == nil || !strings.Contains(err.Error(), `configured tool "missing" is not available`) {
		t.Fatalf("execute error = %v", err)
	}
	if len(model.calls) != 0 {
		t.Fatalf("model calls = %d, want 0", len(model.calls))
	}
}

func TestToolReusesSingleConcurrencySlotForInternalCalls(t *testing.T) {
	model := &scriptedModel{responses: []*llms.ContentResponse{
		{Choices: []*llms.ContentChoice{{ToolCalls: []llms.ToolCall{
			{ID: "first", Type: "function", FunctionCall: &llms.FunctionCall{Name: "blocked", Arguments: `{}`}},
			{ID: "second", Type: "function", FunctionCall: &llms.FunctionCall{Name: "blocked", Arguments: `{}`}},
		}}}},
		{Choices: []*llms.ContentChoice{{Content: "finished"}}},
	}}
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var active atomic.Int32
	var maximumActive atomic.Int32
	blocked := core.NewTool(&llms.FunctionDefinition{Name: "blocked"}, func(context.Context, string) (string, error) {
		current := active.Add(1)
		for {
			maximum := maximumActive.Load()
			if current <= maximum || maximumActive.CompareAndSwap(maximum, current) {
				break
			}
		}
		started <- struct{}{}
		<-release
		active.Add(-1)
		return "ok", nil
	})
	worker, err := NewTool(ToolConfig{
		Name:        "worker",
		Description: "Delegate work.",
		Agent: Config{
			ToolIDs:  []string{"blocked"},
			Parallel: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := core.WithTools(core.WithModel(context.Background(), model), map[string]core.Tool{
		"worker":  worker,
		"blocked": blocked,
	})
	ctx = core.WithToolConcurrencyLimiter(ctx, core.NewConcurrencyLimiter(1), nil)
	done := make(chan llms.MessageContent, 1)
	go func() {
		done <- basenode.ExecuteToolCallMessage(core.NewContext(ctx), llms.ToolCall{
			ID:   "outer",
			Type: "function",
			FunctionCall: &llms.FunctionCall{
				Name:      "worker",
				Arguments: `{"task":"do work"}`,
			},
		})
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first internal tool call did not start")
	}
	select {
	case <-started:
		t.Fatal("second internal tool call bypassed the inherited concurrency slot")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("second internal tool call did not start after capacity was released")
	}
	select {
	case message := <-done:
		if len(message.Parts) != 1 {
			t.Fatalf("tool response = %#v", message)
		}
		response, ok := message.Parts[0].(llms.ToolCallResponse)
		if !ok || response.Content != "finished" {
			t.Fatalf("tool response = %#v, want finished", message.Parts[0])
		}
	case <-time.After(time.Second):
		t.Fatal("nested agent tool execution deadlocked")
	}
	if maximumActive.Load() != 1 {
		t.Fatalf("maximum active internal tools = %d, want 1", maximumActive.Load())
	}
}

type scriptedModel struct {
	mu        sync.Mutex
	responses []*llms.ContentResponse
	calls     [][]llms.MessageContent
	options   []llms.CallOptions
}

func (model *scriptedModel) GenerateContent(_ context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	model.mu.Lock()
	defer model.mu.Unlock()
	if len(model.responses) == 0 {
		return nil, errors.New("scripted model exhausted")
	}
	callOptions := llms.CallOptions{}
	for _, option := range options {
		option(&callOptions)
	}
	model.calls = append(model.calls, cloneMessages(messages))
	model.options = append(model.options, callOptions)
	response := model.responses[0]
	model.responses = model.responses[1:]
	return response, nil
}

func (model *scriptedModel) Call(context.Context, string, ...llms.CallOption) (string, error) {
	return "", errors.New("scripted model Call is not supported")
}

func cloneMessages(messages []llms.MessageContent) []llms.MessageContent {
	cloned := make([]llms.MessageContent, len(messages))
	for index, message := range messages {
		cloned[index] = llms.MessageContent{Role: message.Role, Parts: append([]llms.ContentPart(nil), message.Parts...)}
	}
	return cloned
}
