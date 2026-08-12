package node

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"

	"github.com/tmc/langchaingo/llms"
)

func TestUserInputRequiresResumePath(t *testing.T) {
	t.Parallel()
	target := NewUserInputNode(WithID("input"))
	target.PendingInputPath = state.Path{}

	err := target.Validate()
	if err == nil || !strings.Contains(err.Error(), "requires pending input path") {
		t.Fatalf("validate error = %v, want pending input path error", err)
	}
}

func TestGraphNodeSpecUsesDefaultStatePaths(t *testing.T) {
	t.Parallel()
	target := NewLLMTurnNode(WithID("writer"))
	spec := target.GraphNodeSpec()
	if got := spec.State["conversation"].Path; got != "scopes.writer.conversation" {
		t.Fatalf("conversation default path = %q", got)
	}
	if got := spec.State["output"].Path; got != "shared.final.answer" {
		t.Fatalf("output default path = %q", got)
	}
}

func TestUserInputInterruptsAndConsumesPendingInput(t *testing.T) {
	t.Parallel()
	valuePath := state.Scope("agent", "input")
	pendingInputPath := state.Scope("agent", "pending_input")
	target := NewUserInputNode(WithID("input"))
	target.ValuePath = valuePath
	target.PendingInputPath = pendingInputPath

	_, err := Execute(context.Background(), state.NewState(), target)
	var interrupt *core.NodeInterrupt
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
	value, exists := state.NewAccess(result.State).ReadAny(valuePath)
	if !exists || value != "new question" {
		t.Fatalf("value = %#v, exists = %v", value, exists)
	}
}

func TestUserInputUsesExistingValue(t *testing.T) {
	t.Parallel()
	target := NewUserInputNode(WithID("input"))
	access := state.NewEditingAccess(state.NewState())
	if err := access.SetAny(target.ValuePath, "existing question"); err != nil {
		t.Fatalf("set value: %v", err)
	}
	result, err := Execute(context.Background(), access.State(), target)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	value, exists := state.NewAccess(result.State).ReadAny(target.ValuePath)
	if !exists || value != "existing question" {
		t.Fatalf("value = %#v, exists = %v", value, exists)
	}
}

func TestConversationMessageUsesExplicitBoundRoot(t *testing.T) {
	t.Parallel()
	root := state.Scope("writer", "thread")
	target := NewConversationMessageNode(WithID("message"))
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

func TestConversationMessageStartsNewTurnAtExplicitBoundRoot(t *testing.T) {
	t.Parallel()
	root := state.Scope("writer", "custom_thread")
	access := state.NewEditingAccess(state.NewState())
	view, _ := conversationcap.Bind(access, root)
	_ = view.SetMessages([]llms.MessageContent{llms.TextParts(llms.ChatMessageTypeAI, "old answer")})
	_ = view.SetFinalAnswer("old answer")
	_ = view.SetIterationCount(4)

	target := NewConversationMessageNode(WithID("message"))
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

func TestLLMTurnWritesConversationAndOptionalOutputOnly(t *testing.T) {
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
	target := NewLLMTurnNode(WithID("llm"))
	target.ConversationPath = root
	target.OutputPath = output
	target.ReasoningEffort = "low"
	result, err := Execute(ctx, initial, target)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	answer, _ := state.ReadPath(result.State, output.String())
	if answer != "answer" {
		t.Fatalf("output = %#v", answer)
	}
	if len(model.options) != 1 {
		t.Fatalf("model calls = %d, want 1", len(model.options))
	}
	thinkingConfig := llms.GetThinkingConfig(&model.options[0])
	if thinkingConfig == nil || thinkingConfig.Mode != llms.ThinkingModeLow {
		t.Fatalf("thinking config = %#v, want low reasoning effort", thinkingConfig)
	}
	if _, ok := state.ReadPath(result.State, "shared.execution"); ok {
		t.Fatal("generic LLM touched plan execution state")
	}
	if _, ok := state.ReadPath(result.State, "shared.orchestration"); ok {
		t.Fatal("generic LLM touched orchestration state")
	}
}

func TestLLMTurnContinuesAfterConversationMaxIterations(t *testing.T) {
	t.Parallel()

	root := state.Scope("llm", "conversation")
	access := state.NewEditingAccess(state.NewState())
	view, _ := conversationcap.Bind(access, root)
	_ = view.SetMessages([]llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "question")})
	_ = view.SetMaxIterations(1)
	_ = view.SetIterationCount(1)

	model := &scriptedModel{responses: []*llms.ContentResponse{{Choices: []*llms.ContentChoice{{Content: "answer"}}}}}
	ctx := core.WithModel(context.Background(), model)
	target := NewLLMTurnNode(WithID("llm"))
	target.ConversationPath = root

	result, err := Execute(ctx, access.State(), target)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(model.options) != 1 {
		t.Fatalf("model calls = %d, want 1", len(model.options))
	}
	restored, _ := conversationcap.Bind(state.NewAccess(result.State), root)
	if restored.FinalAnswer() != "answer" || restored.IterationCount() != 2 {
		t.Fatalf("final answer = %q, iteration count = %d", restored.FinalAnswer(), restored.IterationCount())
	}
}

func TestLLMTurnDefaultPromptMaxChars(t *testing.T) {
	t.Parallel()

	if got := NewLLMTurnNode().effectivePromptMaxChars(); got != 200000 {
		t.Fatalf("default prompt max chars = %d, want 200000", got)
	}
	properties := LLMTurnNodeTypeDefinition().NodeTypeSchema.ConfigSchema["properties"].(dsl.JSONSchema)
	promptSchema := properties["prompt_max_chars"].(dsl.JSONSchema)
	if got := promptSchema["default"]; got != 200000 {
		t.Fatalf("prompt_max_chars schema default = %#v, want 200000", got)
	}
	reasoningSchema := properties["reasoning_effort"].(dsl.JSONSchema)
	if got := reasoningSchema["default"]; got != defaultReasoningEffort {
		t.Fatalf("reasoning_effort schema default = %#v, want %q", got, defaultReasoningEffort)
	}
	if got := reasoningSchema["enum"]; !reflect.DeepEqual(got, []string{"auto", "none", "minimal", "low", "medium", "high", "xhigh", "max"}) {
		t.Fatalf("reasoning_effort enum = %#v", got)
	}
}

func TestLLMTurnOnlyInjectsConfiguredTools(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		toolIDs     []string
		wantToolIDs []string
	}{
		{name: "no tool ids"},
		{name: "selected tool", toolIDs: []string{"echo"}, wantToolIDs: []string{"echo"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conversationPath := state.Scope("llm", "conversation")
			access := state.NewEditingAccess(state.NewState())
			view, _ := conversationcap.Bind(access, conversationPath)
			_ = view.SetMessages([]llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "question")})
			_ = view.SetMaxIterations(3)

			model := &scriptedModel{responses: []*llms.ContentResponse{{Choices: []*llms.ContentChoice{{Content: "answer"}}}}}
			availableTools := map[string]core.Tool{
				"echo":  core.NewTool(&llms.FunctionDefinition{Name: "echo"}, nil),
				"other": core.NewTool(&llms.FunctionDefinition{Name: "other"}, nil),
			}
			ctx := core.WithTools(core.WithModel(context.Background(), model), availableTools)
			target := NewLLMTurnNode(WithID("llm"))
			target.ConversationPath = conversationPath
			target.ToolIDs = tt.toolIDs

			if _, err := Execute(ctx, access.State(), target); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if len(model.options) != 1 {
				t.Fatalf("model calls = %d, want 1", len(model.options))
			}
			gotToolIDs := make([]string, 0, len(model.options[0].Tools))
			for _, tool := range model.options[0].Tools {
				if tool.Function != nil {
					gotToolIDs = append(gotToolIDs, tool.Function.Name)
				}
			}
			if strings.Join(gotToolIDs, ",") != strings.Join(tt.wantToolIDs, ",") {
				t.Fatalf("injected tools = %v, want %v", gotToolIDs, tt.wantToolIDs)
			}
		})
	}
}

func TestTextGenerationUsesRawPromptAndWritesOutput(t *testing.T) {
	t.Parallel()

	promptPath := state.Shared("request", "input")
	outputPath := state.Shared("final", "answer")
	access := state.NewEditingAccess(state.NewState())
	if err := access.SetAny(promptPath, "complete this"); err != nil {
		t.Fatalf("set prompt: %v", err)
	}
	model := &scriptedModel{responses: []*llms.ContentResponse{{Choices: []*llms.ContentChoice{{Content: " result"}}}}}
	target := NewTextGenerationNode(WithID("text_generation"))
	target.PromptPath = promptPath
	target.OutputPath = outputPath
	target.MaxTokens = 32
	target.Temperature = 0.2
	target.StopWords = []string{"END"}
	target.ReasoningEffort = "medium"

	result, err := Execute(core.WithModel(context.Background(), model), access.State(), target)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	value, _ := state.ReadPath(result.State, outputPath.String())
	if value != " result" {
		t.Fatalf("output = %#v", value)
	}
	if len(model.completionPrompts) != 1 || model.completionPrompts[0] != "complete this" {
		t.Fatalf("completion prompts = %#v", model.completionPrompts)
	}
	if len(model.calls) != 0 {
		t.Fatalf("chat calls = %d, want 0", len(model.calls))
	}
	if len(model.completionOptions) != 1 {
		t.Fatalf("completion options = %d, want 1", len(model.completionOptions))
	}
	options := model.completionOptions[0]
	if options.MaxTokens != 32 || options.Temperature != 0.2 || len(options.StopWords) != 1 || options.StopWords[0] != "END" {
		t.Fatalf("completion options = %#v", options)
	}
	thinkingConfig := llms.GetThinkingConfig(&options)
	if thinkingConfig == nil || thinkingConfig.Mode != llms.ThinkingModeMedium {
		t.Fatalf("thinking config = %#v, want medium reasoning effort", thinkingConfig)
	}
}

func TestTextGenerationDefaultsAndSchema(t *testing.T) {
	t.Parallel()

	target := NewTextGenerationNode(WithID("text_generation"))
	if got := target.PromptPath.String(); got != "shared.text_generation.prompt" {
		t.Fatalf("prompt default path = %q", got)
	}
	if got := target.OutputPath.String(); got != "shared.text_generation.result" {
		t.Fatalf("output default path = %q", got)
	}
	if target.Temperature != defaultTextGenerationTemperature {
		t.Fatalf("temperature = %v, want %v", target.Temperature, defaultTextGenerationTemperature)
	}
	if target.ReasoningEffort != defaultReasoningEffort {
		t.Fatalf("reasoning effort = %q, want %q", target.ReasoningEffort, defaultReasoningEffort)
	}
	definition := TextGenerationNodeTypeDefinition()
	properties := definition.NodeTypeSchema.ConfigSchema["properties"].(dsl.JSONSchema)
	temperatureSchema := properties["temperature"].(dsl.JSONSchema)
	if got := temperatureSchema["default"]; got != defaultTextGenerationTemperature {
		t.Fatalf("temperature schema default = %#v", got)
	}
	reasoningSchema := properties["reasoning_effort"].(dsl.JSONSchema)
	if got := reasoningSchema["default"]; got != defaultReasoningEffort {
		t.Fatalf("reasoning_effort schema default = %#v, want %q", got, defaultReasoningEffort)
	}
	if len(definition.StatePorts) != 2 || definition.StatePorts[0].Name != "prompt" || definition.StatePorts[0].DefaultPath != "shared.text_generation.prompt" || definition.StatePorts[1].DefaultPath != "shared.text_generation.result" {
		t.Fatalf("state ports = %#v", definition.StatePorts)
	}
}

func TestReasoningEffortValidation(t *testing.T) {
	t.Parallel()

	llmTurn := NewLLMTurnNode(WithID("llm"))
	llmTurn.ReasoningEffort = "extreme"
	if err := llmTurn.Validate(); err == nil || !strings.Contains(err.Error(), "reasoning_effort") {
		t.Fatalf("llm turn validation error = %v, want reasoning_effort error", err)
	}

	textGeneration := NewTextGenerationNode(WithID("text_generation"))
	textGeneration.ReasoningEffort = "extreme"
	if err := textGeneration.Validate(); err == nil || !strings.Contains(err.Error(), "reasoning_effort") {
		t.Fatalf("text generation validation error = %v, want reasoning_effort error", err)
	}
}

func TestReasoningEffortBuildsFromGraphConfig(t *testing.T) {
	t.Parallel()

	llmTurnNode, err := LLMTurnNodeTypeDefinition().Build(&registry.BuildContext{}, registry.ResolvedNodeSpec{
		Spec: dsl.GraphNodeSpec{
			ID:     "llm",
			Config: map[string]any{"reasoning_effort": "max"},
		},
		State: map[string]registry.ResolvedStateBinding{
			"conversation": {Path: state.Scope("llm", "conversation")},
		},
	})
	if err != nil {
		t.Fatalf("build llm turn: %v", err)
	}
	if got := llmTurnNode.(*LLMTurnNode).ReasoningEffort; got != "max" {
		t.Fatalf("llm turn reasoning effort = %q, want max", got)
	}

	textGenerationNode, err := TextGenerationNodeTypeDefinition().Build(&registry.BuildContext{}, registry.ResolvedNodeSpec{
		Spec: dsl.GraphNodeSpec{
			ID:     "text_generation",
			Config: map[string]any{"reasoning_effort": "none"},
		},
		State: map[string]registry.ResolvedStateBinding{
			"prompt": {Path: state.Shared("text_generation", "prompt")},
			"output": {Path: state.Shared("text_generation", "result")},
		},
	})
	if err != nil {
		t.Fatalf("build text generation: %v", err)
	}
	if got := textGenerationNode.(*TextGenerationNode).ReasoningEffort; got != "none" {
		t.Fatalf("text generation reasoning effort = %q, want none", got)
	}
}

func TestTextGenerationRejectsChatOnlyModel(t *testing.T) {
	t.Parallel()

	target := NewTextGenerationNode(WithID("text_generation"))
	access := state.NewEditingAccess(state.NewState())
	if err := access.SetAny(target.PromptPath, "complete"); err != nil {
		t.Fatalf("set prompt: %v", err)
	}
	_, err := Execute(core.WithModel(context.Background(), chatOnlyModel{}), access.State(), target)
	if err == nil || !strings.Contains(err.Error(), "does not support text generation") {
		t.Fatalf("execute error = %v", err)
	}
}

func TestToolExecutionUsesSameExplicitConversationRoot(t *testing.T) {
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
	target := NewToolExecutionNode(WithID("tools"))
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

type scriptedModel struct {
	mu                sync.Mutex
	responses         []*llms.ContentResponse
	calls             [][]llms.MessageContent
	options           []llms.CallOptions
	completionPrompts []string
	completionOptions []llms.CallOptions
}

func (m *scriptedModel) GenerateContent(_ context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.responses) == 0 {
		return nil, errors.New("scripted model exhausted")
	}
	callOptions := llms.CallOptions{}
	for _, option := range options {
		option(&callOptions)
	}
	m.calls = append(m.calls, cloneMessages(messages))
	m.options = append(m.options, callOptions)
	response := m.responses[0]
	m.responses = m.responses[1:]
	return response, nil
}

func (m *scriptedModel) Call(context.Context, string, ...llms.CallOption) (string, error) {
	return "", errors.New("scripted model Call is not supported")
}

func (m *scriptedModel) GenerateCompletion(_ context.Context, prompt string, options ...llms.CallOption) (*llms.ContentResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.responses) == 0 {
		return nil, errors.New("scripted model exhausted")
	}
	callOptions := llms.CallOptions{}
	for _, option := range options {
		option(&callOptions)
	}
	m.completionPrompts = append(m.completionPrompts, prompt)
	m.completionOptions = append(m.completionOptions, callOptions)
	response := m.responses[0]
	m.responses = m.responses[1:]
	return response, nil
}

type chatOnlyModel struct{}

func (chatOnlyModel) GenerateContent(context.Context, []llms.MessageContent, ...llms.CallOption) (*llms.ContentResponse, error) {
	return nil, errors.New("not implemented")
}

func (chatOnlyModel) Call(context.Context, string, ...llms.CallOption) (string, error) {
	return "", errors.New("not implemented")
}
