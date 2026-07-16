package node

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/state"

	langgraph "github.com/smallnest/langgraphgo/graph"
	"github.com/tmc/langchaingo/llms"
)

func TestConversationInputRequiresResumePathWhenInteractive(t *testing.T) {
	t.Parallel()
	target := NewConversationInputNode(WithID("input"))
	target.ConversationPath = state.Scope("agent", "conversation")
	target.InputPath = state.Path{}
	target.PendingInputPath = state.Path{}

	err := target.Validate()
	if err == nil || !strings.Contains(err.Error(), "requires pending input path") {
		t.Fatalf("validate error = %v, want pending input path error", err)
	}
}

func TestGraphNodeSpecUsesDefaultStatePaths(t *testing.T) {
	t.Parallel()
	target := NewLLMNode(WithID("writer"))
	spec := target.GraphNodeSpec()
	if got := spec.State["conversation"].Path; got != "scopes.writer.conversation" {
		t.Fatalf("conversation default path = %q", got)
	}
	if got := spec.State["output"].Path; got != "shared.final.answer" {
		t.Fatalf("output default path = %q", got)
	}
}

func TestConversationInputInterruptsAndConsumesPendingInput(t *testing.T) {
	t.Parallel()
	conversationPath := state.Scope("agent", "conversation")
	pendingInputPath := state.Scope("agent", "pending_input")
	target := NewConversationInputNode(WithID("input"))
	target.ConversationPath = conversationPath
	target.PendingInputPath = pendingInputPath

	_, err := Execute(context.Background(), state.NewState(), target)
	var interrupt *langgraph.NodeInterrupt
	if !errors.As(err, &interrupt) {
		t.Fatalf("execute error = %v, want node interrupt", err)
	}

	initial := state.NewState()
	access := state.NewEditingAccess(initial)
	if err := access.SetAny(pendingInputPath, "new question"); err != nil {
		t.Fatalf("set pending input: %v", err)
	}
	result, err := Execute(context.Background(), access.State(), target)
	if err != nil {
		t.Fatalf("resume execute: %v", err)
	}
	if _, exists := state.NewAccess(result.State).ReadAny(pendingInputPath); exists {
		t.Fatal("pending input was not consumed")
	}
	conversation, err := conversationcap.Bind(state.NewAccess(result.State), conversationPath)
	if err != nil {
		t.Fatalf("bind conversation: %v", err)
	}
	messages := conversation.Messages()
	if len(messages) != 1 || messages[0].Role != llms.ChatMessageTypeHuman || extractText(messages[0]) != "new question" {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestConversationInputUsesExplicitBoundRoot(t *testing.T) {
	t.Parallel()
	root := state.Scope("writer", "thread")
	target := NewConversationInputNode(WithID("input"))
	target.Content = "hello"
	target.ConversationPath = root

	result, err := Execute(context.Background(), state.NewState(), target)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	view, err := conversationcap.Bind(state.NewAccess(result.State), root)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	messages := view.Messages()
	if len(messages) != 1 || messages[0].Role != llms.ChatMessageTypeHuman || extractText(messages[0]) != "hello" {
		t.Fatalf("unexpected messages %#v", messages)
	}
	if _, ok := state.ReadPath(result.State, "scopes.agent.conversation.messages"); ok {
		t.Fatal("input leaked into an implicit default scope")
	}
}

func TestConversationInputStartsNewTurnAtExplicitBoundRoot(t *testing.T) {
	t.Parallel()
	root := state.Scope("writer", "custom_thread")
	access := state.NewEditingAccess(state.NewState())
	view, _ := conversationcap.Bind(access, root)
	_ = view.SetMessages([]llms.MessageContent{llms.TextParts(llms.ChatMessageTypeAI, "old answer")})
	_ = view.SetFinalAnswer("old answer")
	_ = view.SetIterationCount(4)

	target := NewConversationInputNode(WithID("input"))
	target.Content = "new question"
	target.ConversationPath = root
	result, err := Execute(context.Background(), access.State(), target)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	restored, _ := conversationcap.Bind(state.NewAccess(result.State), root)
	if restored.IterationCount() != 0 || restored.FinalAnswer() != "" {
		t.Fatalf("turn state was not reset: iteration=%d final=%q", restored.IterationCount(), restored.FinalAnswer())
	}
	messages := restored.Messages()
	if len(messages) != 2 || messages[1].Role != llms.ChatMessageTypeHuman || extractText(messages[1]) != "new question" {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestLLMWritesConversationAndOptionalOutputOnly(t *testing.T) {
	t.Parallel()
	root := state.Scope("llm", "conversation")
	output := state.Shared("handoff")
	initial := state.NewState()
	access := state.NewEditingAccess(initial)
	view, _ := conversationcap.Bind(access, root)
	_ = view.SetMessages([]llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "question")})
	_ = view.SetMaxIterations(3)
	initial = access.State()

	model := &scriptedModel{responses: []*llms.ContentResponse{{Choices: []*llms.ContentChoice{{Content: "answer"}}}}}
	ctx := core.WithModel(context.Background(), model)
	target := NewLLMNode(WithID("llm"))
	target.ConversationPath = root
	target.OutputPath = output
	result, err := Execute(ctx, initial, target)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	answer, _ := state.ReadPath(result.State, output.String())
	if answer != "answer" {
		t.Fatalf("output = %#v", answer)
	}
	if _, ok := state.ReadPath(result.State, "shared.execution"); ok {
		t.Fatal("generic LLM touched plan execution state")
	}
	if _, ok := state.ReadPath(result.State, "shared.orchestration"); ok {
		t.Fatal("generic LLM touched orchestration state")
	}
}

func TestToolsUsesSameExplicitConversationRoot(t *testing.T) {
	t.Parallel()
	root := state.Scope("loop", "conversation")
	initial := state.NewState()
	access := state.NewEditingAccess(initial)
	view, _ := conversationcap.Bind(access, root)
	_ = view.SetMessages([]llms.MessageContent{{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{
		llms.ToolCall{ID: "call", Type: "function", FunctionCall: &llms.FunctionCall{Name: "echo", Arguments: `{"value":"ok"}`}},
	}}})
	initial = access.State()
	tool := core.NewTool(&llms.FunctionDefinition{Name: "echo"}, func(context.Context, string) (string, error) { return "ok", nil })
	target := NewToolsNode(WithID("tools"))
	target.ConversationPath = root
	ctx := core.WithTools(context.Background(), map[string]core.Tool{"echo": tool})
	result, err := Execute(ctx, initial, target)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	updated, _ := conversationcap.Bind(state.NewAccess(result.State), root)
	messages := updated.Messages()
	if len(messages) != 2 || messages[1].Role != llms.ChatMessageTypeTool {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestAgentUsesExplicitTaskConversationAndResultPaths(t *testing.T) {
	t.Parallel()
	taskPath := state.Shared("request")
	conversationPath := state.Scope("researcher", "conversation")
	resultPath := state.Shared("handoff", "research")
	target := NewAgentNode(WithID("researcher"))
	target.TaskPath = taskPath
	target.ConversationPath = conversationPath
	target.ResultPath = resultPath
	model := &scriptedModel{responses: []*llms.ContentResponse{{Choices: []*llms.ContentChoice{{Content: "research result"}}}}}
	result, err := Execute(core.WithModel(context.Background(), model), state.FromShared(map[string]any{"request": "research this"}), target)
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

type scriptedModel struct {
	mu        sync.Mutex
	responses []*llms.ContentResponse
	calls     [][]llms.MessageContent
}

func (m *scriptedModel) GenerateContent(_ context.Context, messages []llms.MessageContent, _ ...llms.CallOption) (*llms.ContentResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.responses) == 0 {
		return nil, errors.New("scripted model exhausted")
	}
	m.calls = append(m.calls, cloneMessages(messages))
	response := m.responses[0]
	m.responses = m.responses[1:]
	return response, nil
}

func (m *scriptedModel) Call(context.Context, string, ...llms.CallOption) (string, error) {
	return "", errors.New("scripted model Call is not supported")
}
