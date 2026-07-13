package node

import (
	"context"
	"errors"
	"testing"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/state"
	"github.com/dengzii/weaveflow/state/accessors"

	langgraph "github.com/smallnest/langgraphgo/graph"
	"github.com/tmc/langchaingo/llms"
)

func TestFuncNodeExecutesWithStateV2Accessors(t *testing.T) {
	t.Parallel()

	registry, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("new default registry: %v", err)
	}
	initial := state.FromShared(map[string]any{
		accessors.KeyRequest: map[string]any{
			accessors.RequestFieldInput: "ship accessor nodes",
		},
	})
	node := NewFuncNode(Spec{
		ID: "copy_request",
		AccessorUses: []AccessorUse{
			UseRoot(accessors.RequestID.Name()),
			UseRoot(accessors.FinalID.Name()),
		},
	}, func(_ core.Context, access *state.Access) error {
		request, err := state.UseAccessor(access, accessors.RequestID)
		if err != nil {
			return err
		}
		final, err := state.UseAccessor(access, accessors.FinalID)
		if err != nil {
			return err
		}
		return final.SetAnswer(request.Input())
	})

	result, err := Execute(context.Background(), registry, initial, node)
	if err != nil {
		t.Fatalf("execute node: %v", err)
	}

	final, err := state.UseAccessor(state.NewAccess(registry, result.State), accessors.FinalID)
	if err != nil {
		t.Fatalf("use final accessor: %v", err)
	}
	if final.Answer() != "ship accessor nodes" {
		t.Fatalf("unexpected final answer %q", final.Answer())
	}

	ops := result.Patch.Ops()
	if len(ops) != 1 {
		t.Fatalf("expected one patch op, got %#v", ops)
	}
	if ops[0].Path.String() != "shared.final.answer" || ops[0].Value != "ship accessor nodes" {
		t.Fatalf("unexpected patch op %#v", ops[0])
	}
	if len(result.Contract.Fields) == 0 {
		t.Fatal("expected accessor contract fields")
	}
}

func TestHumanMessageNodeUsesScopedConversation(t *testing.T) {
	t.Parallel()

	registry, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("new default registry: %v", err)
	}
	node := NewHumanMessageNode("hello", WithID("human"))

	result, err := Execute(context.Background(), registry, state.NewState(), node)
	if err != nil {
		t.Fatalf("execute human message node: %v", err)
	}

	access := state.NewAccess(registry, result.State).WithScope("agent")
	conversation, err := state.UseAccessor(access, accessors.ConversationID)
	if err != nil {
		t.Fatalf("use conversation accessor: %v", err)
	}
	if len(conversation.Messages()) != 1 {
		t.Fatalf("expected one scoped message, got %#v", conversation.Messages())
	}
	if result.Contract.Fields[0].Path.String() != "scopes.agent.conversation.messages" {
		t.Fatalf("expected scoped conversation contract, got %#v", result.Contract.Fields[0])
	}
}

func TestHumanMessageNodeInterruptsWhenConversationIsEmpty(t *testing.T) {
	t.Parallel()

	registry, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("new default registry: %v", err)
	}
	node := NewHumanMessageNode("", WithID("human"))

	_, err = Execute(context.Background(), registry, state.NewState(), node)
	if err == nil {
		t.Fatal("expected human message node interrupt")
	}
	var interrupt *langgraph.NodeInterrupt
	if !errors.As(err, &interrupt) {
		t.Fatalf("expected NodeInterrupt, got %T: %v", err, err)
	}
	if interrupt.Node != "human" {
		t.Fatalf("interrupt node = %q, want human", interrupt.Node)
	}
}

func TestHumanMessageNodeConsumesPendingInput(t *testing.T) {
	t.Parallel()

	registry, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("new default registry: %v", err)
	}
	initial := state.NewState()
	if err := state.SetPath(initial, state.Scope(DefaultScope, PendingHumanInputStateKey).String(), "hello from resume"); err != nil {
		t.Fatalf("set pending input: %v", err)
	}
	node := NewHumanMessageNode("", WithID("human"))

	result, err := Execute(context.Background(), registry, initial, node)
	if err != nil {
		t.Fatalf("execute human message node: %v", err)
	}

	access := state.NewAccess(registry, result.State).WithScope(DefaultScope)
	if _, exists := access.ReadAny(state.Scope(DefaultScope, PendingHumanInputStateKey)); exists {
		t.Fatal("pending human input was not consumed")
	}
	conversation, err := state.UseAccessor(access, accessors.ConversationID)
	if err != nil {
		t.Fatalf("use conversation accessor: %v", err)
	}
	messages := conversation.Messages()
	if len(messages) != 1 {
		t.Fatalf("expected one message, got %#v", messages)
	}
	if messages[0].Role != llms.ChatMessageTypeHuman {
		t.Fatalf("message role = %q, want human", messages[0].Role)
	}
}

func TestExecuteRejectsUnregisteredAccessor(t *testing.T) {
	t.Parallel()

	node := NewFuncNode(Spec{
		ID:           "bad",
		AccessorUses: []AccessorUse{UseRoot("missing")},
	}, func(core.Context, *state.Access) error {
		return nil
	})

	_, err := Execute(context.Background(), state.NewRegistry(), state.NewState(), node)
	if err == nil {
		t.Fatal("expected unregistered accessor error")
	}
}

func TestRequestToFinalAnswerNode(t *testing.T) {
	t.Parallel()

	registry, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("new default registry: %v", err)
	}
	initial := state.FromShared(map[string]any{
		accessors.KeyRequest: map[string]any{
			accessors.RequestFieldInput: "from request",
		},
	})

	result, err := Execute(context.Background(), registry, initial, NewRequestToFinalAnswerNode(WithID("final")))
	if err != nil {
		t.Fatalf("execute request to final node: %v", err)
	}
	final, err := state.UseAccessor(state.NewAccess(registry, result.State), accessors.FinalID)
	if err != nil {
		t.Fatalf("use final accessor: %v", err)
	}
	if final.Answer() != "from request" {
		t.Fatalf("unexpected final answer %q", final.Answer())
	}
}

func TestMappedSubgraphNodeUsesStateV2PathMappings(t *testing.T) {
	t.Parallel()

	registry, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("new default registry: %v", err)
	}
	initial := state.FromShared(map[string]any{
		accessors.KeyRequest: map[string]any{
			accessors.RequestFieldInput: "subgraph input",
		},
	})

	node := NewMappedSubgraphNode(WithID("subgraph"))
	node.InputMappings = []PathMapping{
		{From: state.Shared(accessors.KeyRequest, accessors.RequestFieldInput), To: state.Shared(accessors.KeyRequest, accessors.RequestFieldInput)},
	}
	node.OutputMappings = []PathMapping{
		{From: state.Shared(accessors.KeyFinal, accessors.FinalFieldAnswer), To: state.Shared(accessors.KeyFinal, accessors.FinalFieldAnswer)},
	}
	node.InvokeSubgraph = func(_ context.Context, input *state.State) (*state.State, error) {
		access := state.NewAccess(registry, input)
		value, _ := access.ReadAny(state.Shared(accessors.KeyRequest, accessors.RequestFieldInput))
		output := state.NewEditingAccess(registry, state.NewState())
		if err := output.SetAny(state.Shared(accessors.KeyFinal, accessors.FinalFieldAnswer), value.(string)+" done"); err != nil {
			return nil, err
		}
		return output.State(), nil
	}

	result, err := Execute(context.Background(), registry, initial, node)
	if err != nil {
		t.Fatalf("execute mapped subgraph: %v", err)
	}
	final, err := state.UseAccessor(state.NewAccess(registry, result.State), accessors.FinalID)
	if err != nil {
		t.Fatalf("use final accessor: %v", err)
	}
	if final.Answer() != "subgraph input done" {
		t.Fatalf("unexpected mapped answer %q", final.Answer())
	}
	if got := result.Contract.ReadPaths()[0].String(); got != "shared.request.input" {
		t.Fatalf("unexpected read path %q", got)
	}
	if got := result.Contract.WritePaths()[0].String(); got != "shared.final.answer" {
		t.Fatalf("unexpected write path %q", got)
	}
}

func TestLLMNodeWritesScopedConversationFinalAnswer(t *testing.T) {
	t.Parallel()

	registry, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("new default registry: %v", err)
	}
	seed := state.NewEditingAccess(registry, state.NewState()).WithScope("agent")
	conversation, err := state.UseAccessor(seed, accessors.ConversationID)
	if err != nil {
		t.Fatalf("use conversation accessor: %v", err)
	}
	if err := conversation.SetMessages([]llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "question")}); err != nil {
		t.Fatalf("seed conversation: %v", err)
	}

	model := &scriptedModel{responses: []*llms.ContentResponse{{
		Choices: []*llms.ContentChoice{{Content: "answer"}},
	}}}
	ctx := core.NewContext(core.WithModel(context.Background(), model))
	result, err := Execute(ctx, registry, seed.State(), NewLLMNode(WithID("llm")))
	if err != nil {
		t.Fatalf("execute llm node: %v", err)
	}

	access := state.NewAccess(registry, result.State).WithScope("agent")
	updated, err := state.UseAccessor(access, accessors.ConversationID)
	if err != nil {
		t.Fatalf("use conversation accessor: %v", err)
	}
	if updated.FinalAnswer() != "answer" {
		t.Fatalf("unexpected final answer %q", updated.FinalAnswer())
	}
	if len(updated.Messages()) != 2 {
		t.Fatalf("expected two messages, got %#v", updated.Messages())
	}
}

func TestLLMNodeSystemPromptConfigurationRoundTrip(t *testing.T) {
	t.Parallel()

	llmNode := NewLLMNode(WithID("llm"))
	llmNode.SystemPrompt = "You are a concise assistant."
	spec := llmNode.GraphNodeSpec()
	if got := spec.Config["system_prompt"]; got != llmNode.SystemPrompt {
		t.Fatalf("system_prompt config = %#v, want %q", got, llmNode.SystemPrompt)
	}

	definition := LLMNodeTypeDefinition()
	properties, ok := definition.ConfigSchema["properties"].(dsl.JSONSchema)
	if !ok {
		t.Fatalf("LLM config properties schema = %#v", definition.ConfigSchema["properties"])
	}
	systemPromptSchema, ok := properties["system_prompt"].(dsl.JSONSchema)
	if !ok || systemPromptSchema["type"] != "string" || systemPromptSchema["x-control"] != "textarea" {
		t.Fatalf("system_prompt schema = %#v, want textarea string property", properties["system_prompt"])
	}

	built, err := definition.Build(nil, spec)
	if err != nil {
		t.Fatalf("build LLM node: %v", err)
	}
	builtLLM, ok := built.(*LLMNode)
	if !ok {
		t.Fatalf("built node type = %T, want *LLMNode", built)
	}
	if builtLLM.SystemPrompt != llmNode.SystemPrompt {
		t.Fatalf("built system prompt = %q, want %q", builtLLM.SystemPrompt, llmNode.SystemPrompt)
	}
}

func TestAgentNodeSystemPromptSchemaUsesTextarea(t *testing.T) {
	t.Parallel()

	definition := AgentNodeTypeDefinition()
	properties, ok := definition.ConfigSchema["properties"].(dsl.JSONSchema)
	if !ok {
		t.Fatalf("agent config properties schema = %#v", definition.ConfigSchema["properties"])
	}
	systemPromptSchema, ok := properties["system_prompt"].(dsl.JSONSchema)
	if !ok || systemPromptSchema["type"] != "string" || systemPromptSchema["x-control"] != "textarea" {
		t.Fatalf("system_prompt schema = %#v, want textarea string property", properties["system_prompt"])
	}
}

func TestLLMNodeSeedsConfiguredSystemPrompt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		initial        []llms.MessageContent
		expectedPrompt string
	}{
		{
			name: "adds prompt when missing",
			initial: []llms.MessageContent{
				llms.TextParts(llms.ChatMessageTypeHuman, "question"),
			},
			expectedPrompt: "configured system prompt",
		},
		{
			name: "preserves existing prompt",
			initial: []llms.MessageContent{
				llms.TextParts(llms.ChatMessageTypeSystem, "existing system prompt"),
				llms.TextParts(llms.ChatMessageTypeHuman, "question"),
			},
			expectedPrompt: "existing system prompt",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry, err := NewDefaultRegistry()
			if err != nil {
				t.Fatalf("new default registry: %v", err)
			}
			seed := state.NewEditingAccess(registry, state.NewState()).WithScope("agent")
			conversation, err := state.UseAccessor(seed, accessors.ConversationID)
			if err != nil {
				t.Fatalf("use conversation accessor: %v", err)
			}
			if err := conversation.SetMessages(test.initial); err != nil {
				t.Fatalf("seed conversation: %v", err)
			}

			model := &scriptedModel{responses: []*llms.ContentResponse{{
				Choices: []*llms.ContentChoice{{Content: "answer"}},
			}}}
			ctx := core.NewContext(core.WithModel(context.Background(), model))
			llmNode := NewLLMNode(WithID("llm"))
			llmNode.SystemPrompt = "configured system prompt"

			result, err := Execute(ctx, registry, seed.State(), llmNode)
			if err != nil {
				t.Fatalf("execute LLM node: %v", err)
			}
			if len(model.requests) != 1 {
				t.Fatalf("model requests = %d, want 1", len(model.requests))
			}
			assertSingleSystemPrompt(t, model.requests[0], test.expectedPrompt)

			access := state.NewAccess(registry, result.State).WithScope("agent")
			updated, err := state.UseAccessor(access, accessors.ConversationID)
			if err != nil {
				t.Fatalf("use updated conversation accessor: %v", err)
			}
			assertSingleSystemPrompt(t, updated.Messages(), test.expectedPrompt)
		})
	}
}

func TestAgentNodeReadsRequestAndWritesFinalAccessor(t *testing.T) {
	t.Parallel()

	registry, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("new default registry: %v", err)
	}
	initial := state.FromShared(map[string]any{
		accessors.KeyRequest: map[string]any{
			accessors.RequestFieldInput: "agent task",
		},
	})
	model := &scriptedModel{responses: []*llms.ContentResponse{{
		Choices: []*llms.ContentChoice{{Content: "agent answer"}},
	}}}
	ctx := core.NewContext(core.WithModel(context.Background(), model))

	result, err := Execute(ctx, registry, initial, NewAgentNode(WithScope("worker"), WithID("agent")))
	if err != nil {
		t.Fatalf("execute agent node: %v", err)
	}
	final, err := state.UseAccessor(state.NewAccess(registry, result.State), accessors.FinalID)
	if err != nil {
		t.Fatalf("use final accessor: %v", err)
	}
	if final.Answer() != "agent answer" {
		t.Fatalf("unexpected final answer %q", final.Answer())
	}
}

func TestLLMNodeUsesConfiguredModelID(t *testing.T) {
	t.Parallel()

	registry, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("new default registry: %v", err)
	}
	seed := state.NewEditingAccess(registry, state.NewState()).WithScope("agent")
	conversation, err := state.UseAccessor(seed, accessors.ConversationID)
	if err != nil {
		t.Fatalf("use conversation accessor: %v", err)
	}
	if err := conversation.SetMessages([]llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "question")}); err != nil {
		t.Fatalf("seed conversation: %v", err)
	}

	defaultModel := &scriptedModel{responses: []*llms.ContentResponse{{
		Choices: []*llms.ContentChoice{{Content: "default answer"}},
	}}}
	selectedModel := &scriptedModel{responses: []*llms.ContentResponse{{
		Choices: []*llms.ContentChoice{{Content: "selected answer"}},
	}}}
	ctx := core.NewContext(core.WithModels(context.Background(), map[string]llms.Model{
		core.DefaultModelID: defaultModel,
		"selected":          selectedModel,
	}))
	llmNode := NewLLMNode(WithID("llm"))
	llmNode.ModelID = "selected"

	result, err := Execute(ctx, registry, seed.State(), llmNode)
	if err != nil {
		t.Fatalf("execute llm node: %v", err)
	}
	if defaultModel.calls != 0 {
		t.Fatalf("default model calls = %d, want 0", defaultModel.calls)
	}
	if selectedModel.calls != 1 {
		t.Fatalf("selected model calls = %d, want 1", selectedModel.calls)
	}

	access := state.NewAccess(registry, result.State).WithScope("agent")
	updated, err := state.UseAccessor(access, accessors.ConversationID)
	if err != nil {
		t.Fatalf("use conversation accessor: %v", err)
	}
	if updated.FinalAnswer() != "selected answer" {
		t.Fatalf("unexpected final answer %q", updated.FinalAnswer())
	}
}

func TestAgentNodeUsesConfiguredModelID(t *testing.T) {
	t.Parallel()

	registry, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("new default registry: %v", err)
	}
	initial := state.FromShared(map[string]any{
		accessors.KeyRequest: map[string]any{
			accessors.RequestFieldInput: "agent task",
		},
	})
	defaultModel := &scriptedModel{responses: []*llms.ContentResponse{{
		Choices: []*llms.ContentChoice{{Content: "default answer"}},
	}}}
	selectedModel := &scriptedModel{responses: []*llms.ContentResponse{{
		Choices: []*llms.ContentChoice{{Content: "selected agent answer"}},
	}}}
	ctx := core.NewContext(core.WithModels(context.Background(), map[string]llms.Model{
		core.DefaultModelID: defaultModel,
		"selected":          selectedModel,
	}))
	agentNode := NewAgentNode(WithScope("worker"), WithID("agent"))
	agentNode.ModelID = "selected"

	result, err := Execute(ctx, registry, initial, agentNode)
	if err != nil {
		t.Fatalf("execute agent node: %v", err)
	}
	if defaultModel.calls != 0 {
		t.Fatalf("default model calls = %d, want 0", defaultModel.calls)
	}
	if selectedModel.calls != 1 {
		t.Fatalf("selected model calls = %d, want 1", selectedModel.calls)
	}
	final, err := state.UseAccessor(state.NewAccess(registry, result.State), accessors.FinalID)
	if err != nil {
		t.Fatalf("use final accessor: %v", err)
	}
	if final.Answer() != "selected agent answer" {
		t.Fatalf("unexpected final answer %q", final.Answer())
	}
}

type scriptedModel struct {
	responses []*llms.ContentResponse
	requests  [][]llms.MessageContent
	calls     int
}

func (m *scriptedModel) GenerateContent(_ context.Context, messages []llms.MessageContent, _ ...llms.CallOption) (*llms.ContentResponse, error) {
	if m.calls >= len(m.responses) {
		return nil, errors.New("scripted model exhausted")
	}
	m.requests = append(m.requests, cloneMessages(messages))
	response := m.responses[m.calls]
	m.calls++
	return response, nil
}

func (m *scriptedModel) Call(context.Context, string, ...llms.CallOption) (string, error) {
	return "", errors.New("scripted model Call is not supported")
}

func assertSingleSystemPrompt(t *testing.T, messages []llms.MessageContent, expected string) {
	t.Helper()

	systemMessages := make([]llms.MessageContent, 0, 1)
	for _, message := range messages {
		if message.Role == llms.ChatMessageTypeSystem {
			systemMessages = append(systemMessages, message)
		}
	}
	if len(systemMessages) != 1 {
		t.Fatalf("system message count = %d, want 1 in %#v", len(systemMessages), messages)
	}
	if len(messages) == 0 || messages[0].Role != llms.ChatMessageTypeSystem {
		t.Fatalf("first message is not the system prompt in %#v", messages)
	}
	if got := extractText(messages[0]); got != expected {
		t.Fatalf("system prompt = %q, want %q", got, expected)
	}
}
