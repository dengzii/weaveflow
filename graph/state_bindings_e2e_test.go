package graph

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/dengzii/weaveflow/builtin"
	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"

	"github.com/tmc/langchaingo/llms"
)

func TestGraphV2TwoAgentHandoffUsesIsolatedConversations(t *testing.T) {
	t.Parallel()
	firstModel := &graphScriptedModel{responses: []*llms.ContentResponse{contentResponse("research result")}}
	secondModel := &graphScriptedModel{responses: []*llms.ContentResponse{contentResponse("final answer")}}
	def := dsl.GraphDefinition{
		Version:      dsl.GraphDefinitionVersion,
		Name:         "two-agent-handoff",
		StateModules: protocolModuleRefs(),
		EntryPoint:   "researcher",
		FinishPoint:  "writer",
		Nodes: []dsl.GraphNodeSpec{
			{
				ID: "researcher", Type: node.NodeTypeAgent,
				Config: map[string]any{"model_id": "research", "max_iterations": 2},
				State: map[string]dsl.StateBinding{
					"task":         binding("shared.request.input"),
					"conversation": binding("scopes.researcher.conversation"),
					"result":       binding("shared.handoff.research"),
				},
			},
			{
				ID: "writer", Type: node.NodeTypeAgent,
				Config: map[string]any{"model_id": "writer", "max_iterations": 2},
				State: map[string]dsl.StateBinding{
					"task":         binding("shared.handoff.research"),
					"conversation": binding("scopes.writer.conversation"),
					"result":       binding("shared.final.answer"),
				},
			},
		},
		Edges: []dsl.GraphEdgeSpec{{From: "researcher", To: "writer"}},
	}

	result := runBoundGraph(t, def, state.FromShared(map[string]any{
		"request": map[string]any{"input": "research the topic"},
	}), map[string]llms.Model{"research": firstModel, "writer": secondModel}, nil)

	answer, _ := state.ReadPath(result, "shared.final.answer")
	if answer != "final answer" {
		t.Fatalf("final answer = %#v", answer)
	}
	researchConversation, _ := conversationcap.Bind(state.NewAccess(result), state.Scope("researcher", "conversation"))
	writerConversation, _ := conversationcap.Bind(state.NewAccess(result), state.Scope("writer", "conversation"))
	if len(researchConversation.Messages()) != 2 || len(writerConversation.Messages()) != 2 {
		t.Fatalf("conversation sizes research=%d writer=%d", len(researchConversation.Messages()), len(writerConversation.Messages()))
	}
	if got := firstHumanText(secondModel.Calls()); got != "research result" {
		t.Fatalf("writer task = %q", got)
	}
	if firstHumanText(firstModel.Calls()) == firstHumanText(secondModel.Calls()) {
		t.Fatal("agent conversations were not isolated")
	}
}

func TestGraphV2TwoAgentsCanShareConversationRoot(t *testing.T) {
	t.Parallel()
	firstModel := &graphScriptedModel{responses: []*llms.ContentResponse{contentResponse("research result")}}
	secondModel := &graphScriptedModel{responses: []*llms.ContentResponse{contentResponse("final answer")}}
	conversationPath := "shared.team_conversation"
	def := dsl.GraphDefinition{
		Version:      dsl.GraphDefinitionVersion,
		Name:         "shared-agent-conversation",
		StateModules: protocolModuleRefs(),
		EntryPoint:   "researcher",
		FinishPoint:  "writer",
		Nodes: []dsl.GraphNodeSpec{
			{
				ID: "researcher", Type: node.NodeTypeAgent,
				Config: map[string]any{"model_id": "research", "max_iterations": 2},
				State: map[string]dsl.StateBinding{
					"task":         binding("shared.request.input"),
					"conversation": binding(conversationPath),
					"result":       binding("shared.handoff.research"),
				},
			},
			{
				ID: "writer", Type: node.NodeTypeAgent,
				Config: map[string]any{"model_id": "writer", "max_iterations": 2},
				State: map[string]dsl.StateBinding{
					"task":         binding("shared.handoff.research"),
					"conversation": binding(conversationPath),
					"result":       binding("shared.final.answer"),
				},
			},
		},
		Edges: []dsl.GraphEdgeSpec{{From: "researcher", To: "writer"}},
	}

	result := runBoundGraph(t, def, state.FromShared(map[string]any{
		"request": map[string]any{"input": "research the topic"},
	}), map[string]llms.Model{"research": firstModel, "writer": secondModel}, nil)

	answer, _ := state.ReadPath(result, "shared.final.answer")
	if answer != "final answer" {
		t.Fatalf("final answer = %#v", answer)
	}
	conversation, err := conversationcap.Bind(state.NewAccess(result), state.Shared("team_conversation"))
	if err != nil {
		t.Fatalf("bind conversation: %v", err)
	}
	messages := conversation.Messages()
	roles := []llms.ChatMessageType{llms.ChatMessageTypeHuman, llms.ChatMessageTypeAI, llms.ChatMessageTypeAI}
	texts := []string{"research the topic", "research result", "final answer"}
	if len(messages) != len(roles) {
		t.Fatalf("messages = %#v", messages)
	}
	for index, role := range roles {
		if messages[index].Role != role || graphMessageText(messages[index]) != texts[index] {
			t.Fatalf("message %d = role %q text %q, want role %q text %q", index, messages[index].Role, graphMessageText(messages[index]), role, texts[index])
		}
	}
	if conversation.FinalAnswer() != "final answer" {
		t.Fatalf("conversation final answer = %q", conversation.FinalAnswer())
	}
	writerCalls := secondModel.Calls()
	if len(writerCalls) != 1 || len(writerCalls[0]) != 2 || writerCalls[0][1].Role != llms.ChatMessageTypeAI || graphMessageText(writerCalls[0][1]) != "research result" {
		t.Fatalf("writer calls = %#v", writerCalls)
	}
}

func TestGraphV2MultipleLLMTurnsUseDifferentModelsAndConversationRoots(t *testing.T) {
	t.Parallel()
	firstModel := &graphScriptedModel{responses: []*llms.ContentResponse{contentResponse("first output")}}
	secondModel := &graphScriptedModel{responses: []*llms.ContentResponse{contentResponse("second output")}}
	def := dsl.GraphDefinition{
		Version:      dsl.GraphDefinitionVersion,
		Name:         "isolated-multi-llm",
		StateModules: protocolModuleRefs(),
		EntryPoint:   "input_one",
		FinishPoint:  "llm_two",
		Nodes: []dsl.GraphNodeSpec{
			{
				ID: "input_one", Type: node.NodeTypeConversationMessage,
				State: map[string]dsl.StateBinding{
					"input":        binding("shared.request.input"),
					"conversation": binding("scopes.llm_one.conversation"),
				},
			},
			{
				ID: "llm_one", Type: node.NodeTypeLLMTurn,
				Config: map[string]any{"model_id": "first"},
				State: map[string]dsl.StateBinding{
					"conversation": binding("scopes.llm_one.conversation"),
					"output":       binding("shared.handoff.llm_one"),
				},
			},
			{
				ID: "input_two", Type: node.NodeTypeConversationMessage,
				State: map[string]dsl.StateBinding{
					"input":        binding("shared.handoff.llm_one"),
					"conversation": binding("scopes.llm_two.conversation"),
				},
			},
			{
				ID: "llm_two", Type: node.NodeTypeLLMTurn,
				Config: map[string]any{"model_id": "second"},
				State: map[string]dsl.StateBinding{
					"conversation": binding("scopes.llm_two.conversation"),
					"output":       binding("shared.final.answer"),
				},
			},
		},
		Edges: []dsl.GraphEdgeSpec{
			{From: "input_one", To: "llm_one"},
			{From: "llm_one", To: "input_two"},
			{From: "input_two", To: "llm_two"},
		},
	}

	result := runBoundGraph(t, def, state.FromShared(map[string]any{
		"request": map[string]any{"input": "initial input"},
	}), map[string]llms.Model{"first": firstModel, "second": secondModel}, nil)

	answer, _ := state.ReadPath(result, "shared.final.answer")
	if answer != "second output" {
		t.Fatalf("final answer = %#v", answer)
	}
	if got := firstHumanText(firstModel.Calls()); got != "initial input" {
		t.Fatalf("first model input = %q", got)
	}
	if got := firstHumanText(secondModel.Calls()); got != "first output" {
		t.Fatalf("second model input = %q", got)
	}
	firstConversation, _ := conversationcap.Bind(state.NewAccess(result), state.Scope("llm_one", "conversation"))
	secondConversation, _ := conversationcap.Bind(state.NewAccess(result), state.Scope("llm_two", "conversation"))
	if len(firstConversation.Messages()) != 2 || len(secondConversation.Messages()) != 2 {
		t.Fatalf("conversation sizes first=%d second=%d", len(firstConversation.Messages()), len(secondConversation.Messages()))
	}
}

func TestGraphV2MultipleLLMTurnsCanShareConversationRoot(t *testing.T) {
	t.Parallel()
	firstModel := &graphScriptedModel{responses: []*llms.ContentResponse{contentResponse("first output")}}
	secondModel := &graphScriptedModel{responses: []*llms.ContentResponse{contentResponse("second output")}}
	conversationPath := "shared.conversation"
	def := dsl.GraphDefinition{
		Version:      dsl.GraphDefinitionVersion,
		Name:         "shared-multi-llm",
		StateModules: protocolModuleRefs(),
		EntryPoint:   "input_one",
		FinishPoint:  "llm_two",
		Nodes: []dsl.GraphNodeSpec{
			{
				ID: "input_one", Type: node.NodeTypeConversationMessage,
				State: map[string]dsl.StateBinding{
					"input":        binding("shared.request.input"),
					"conversation": binding(conversationPath),
				},
			},
			{
				ID: "llm_one", Type: node.NodeTypeLLMTurn,
				Config: map[string]any{"model_id": "first"},
				State: map[string]dsl.StateBinding{
					"conversation": binding(conversationPath),
					"output":       binding("shared.handoff.llm_one"),
				},
			},
			{
				ID: "input_two", Type: node.NodeTypeConversationMessage,
				State: map[string]dsl.StateBinding{
					"input":        binding("shared.handoff.llm_one"),
					"conversation": binding(conversationPath),
				},
			},
			{
				ID: "llm_two", Type: node.NodeTypeLLMTurn,
				Config: map[string]any{"model_id": "second"},
				State: map[string]dsl.StateBinding{
					"conversation": binding(conversationPath),
					"output":       binding("shared.final.answer"),
				},
			},
		},
		Edges: []dsl.GraphEdgeSpec{
			{From: "input_one", To: "llm_one"},
			{From: "llm_one", To: "input_two"},
			{From: "input_two", To: "llm_two"},
		},
	}

	result := runBoundGraph(t, def, state.FromShared(map[string]any{
		"request": map[string]any{"input": "initial input"},
	}), map[string]llms.Model{"first": firstModel, "second": secondModel}, nil)

	answer, _ := state.ReadPath(result, "shared.final.answer")
	if answer != "second output" {
		t.Fatalf("final answer = %#v", answer)
	}
	conversation, err := conversationcap.Bind(state.NewAccess(result), state.Shared("conversation"))
	if err != nil {
		t.Fatalf("bind conversation: %v", err)
	}
	messages := conversation.Messages()
	roles := []llms.ChatMessageType{llms.ChatMessageTypeHuman, llms.ChatMessageTypeAI, llms.ChatMessageTypeHuman, llms.ChatMessageTypeAI}
	texts := []string{"initial input", "first output", "first output", "second output"}
	if len(messages) != len(roles) {
		t.Fatalf("messages = %#v", messages)
	}
	for index, role := range roles {
		if messages[index].Role != role || graphMessageText(messages[index]) != texts[index] {
			t.Fatalf("message %d = role %q text %q, want role %q text %q", index, messages[index].Role, graphMessageText(messages[index]), role, texts[index])
		}
	}
	if conversation.FinalAnswer() != "second output" {
		t.Fatalf("conversation final answer = %q", conversation.FinalAnswer())
	}
	secondCalls := secondModel.Calls()
	if len(secondCalls) != 1 || len(secondCalls[0]) != 3 || secondCalls[0][2].Role != llms.ChatMessageTypeHuman || graphMessageText(secondCalls[0][2]) != "first output" {
		t.Fatalf("second model calls = %#v", secondCalls)
	}
}

func TestGraphV2LLMTurnToolExecutionAndConditionShareConversationBinding(t *testing.T) {
	t.Parallel()
	model := &graphScriptedModel{responses: []*llms.ContentResponse{
		{
			Choices: []*llms.ContentChoice{{ToolCalls: []llms.ToolCall{{
				ID: "call-1", Type: "function",
				FunctionCall: &llms.FunctionCall{Name: "echo", Arguments: `{"value":"ok"}`},
			}}}},
		},
		contentResponse("tool loop complete"),
	}}
	var toolMu sync.Mutex
	toolCalls := 0
	echo := core.NewTool(&llms.FunctionDefinition{Name: "echo"}, func(context.Context, string) (string, error) {
		toolMu.Lock()
		toolCalls++
		toolMu.Unlock()
		return "ok", nil
	})
	conversationPath := "shared.agent_thread"
	def := dsl.GraphDefinition{
		Version:      dsl.GraphDefinitionVersion,
		Name:         "shared-tool-loop",
		StateModules: protocolModuleRefs(),
		EntryPoint:   "input",
		Nodes: []dsl.GraphNodeSpec{
			{
				ID: "input", Type: node.NodeTypeConversationMessage,
				State: map[string]dsl.StateBinding{
					"input":        binding("shared.request.input"),
					"conversation": binding(conversationPath),
				},
			},
			{
				ID: "llm", Type: node.NodeTypeLLMTurn,
				Config: map[string]any{"model_id": "loop", "tool_ids": []any{"echo"}},
				State: map[string]dsl.StateBinding{
					"conversation": binding(conversationPath),
					"output":       binding("shared.final.answer"),
				},
			},
			{
				ID: "tools", Type: node.NodeTypeToolExecution,
				Config: map[string]any{"tool_ids": []any{"echo"}, "parallel": false},
				State:  map[string]dsl.StateBinding{"conversation": binding(conversationPath)},
			},
		},
		Edges: []dsl.GraphEdgeSpec{
			{From: "input", To: "llm"},
			{
				From: "llm", To: "tools",
				Condition: &dsl.GraphConditionSpec{
					Type:  builtin.ConditionTypeConversationHasToolCalls,
					State: map[string]dsl.StateBinding{"conversation": binding(conversationPath)},
				},
			},
			{From: "llm", To: dsl.EndNodeRef},
			{From: "tools", To: "llm"},
		},
	}

	result := runBoundGraph(t, def, state.FromShared(map[string]any{
		"request": map[string]any{"input": "use the tool"},
	}), map[string]llms.Model{"loop": model}, map[string]core.Tool{"echo": echo})

	toolMu.Lock()
	gotToolCalls := toolCalls
	toolMu.Unlock()
	if gotToolCalls != 1 {
		t.Fatalf("tool calls = %d", gotToolCalls)
	}
	conversation, err := conversationcap.Bind(state.NewAccess(result), state.Shared("agent_thread"))
	if err != nil {
		t.Fatalf("bind conversation: %v", err)
	}
	messages := conversation.Messages()
	if len(messages) != 4 {
		t.Fatalf("messages = %#v", messages)
	}
	roles := []llms.ChatMessageType{llms.ChatMessageTypeHuman, llms.ChatMessageTypeAI, llms.ChatMessageTypeTool, llms.ChatMessageTypeAI}
	for index, role := range roles {
		if messages[index].Role != role {
			t.Fatalf("message %d role = %q, want %q", index, messages[index].Role, role)
		}
	}
	answer, _ := state.ReadPath(result, "shared.final.answer")
	if answer != "tool loop complete" || conversation.FinalAnswer() != "tool loop complete" {
		t.Fatalf("answers output=%#v conversation=%q", answer, conversation.FinalAnswer())
	}
}

func TestGraphV2SubgraphUsesExplicitInputAndOutputBindings(t *testing.T) {
	t.Parallel()
	const moduleName = "test.subgraph"
	reg := builtin.NewDefaultRegistry()
	if err := reg.RegisterStateModule(dsl.StateModuleDefinition{
		Name: moduleName, Version: "1",
		Fields: []dsl.StateFieldDefinition{
			{Path: "shared.child_payload", Schema: dsl.JSONSchema{"type": "object"}},
			{Path: "shared.value", Schema: dsl.JSONSchema{"type": "string"}},
		},
	}); err != nil {
		t.Fatalf("register subgraph state module: %v", err)
	}
	if err := reg.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type: "copy_value",
			StatePorts: []dsl.StatePortDefinition{
				{Name: "input", Required: true, Schema: dsl.JSONSchema{"type": "string"}, Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace},
				{Name: "output", Required: true, Schema: dsl.JSONSchema{"type": "string"}, Mode: dsl.StateAccessWrite, MergeStrategy: dsl.StateMergeReplace},
			},
		},
		Build: func(_ *registry.BuildContext, spec registry.ResolvedNodeSpec) (core.Node, error) {
			return &graphCopyNode{
				NodeInfo: core.NodeInfo{NodeID: spec.Spec.ID},
				input:    spec.State["input"].Path,
				output:   spec.State["output"].Path,
			}, nil
		},
	}); err != nil {
		t.Fatalf("register copy node: %v", err)
	}
	child := dsl.GraphDefinition{
		Version:      dsl.GraphDefinitionVersion,
		Name:         "child",
		StateModules: []dsl.StateModuleRef{{Name: moduleName, Version: "1"}},
		EntryPoint:   "copy",
		FinishPoint:  "copy",
		Nodes: []dsl.GraphNodeSpec{{
			ID: "copy", Type: "copy_value",
			State: map[string]dsl.StateBinding{
				"input":  binding("shared.value"),
				"output": binding("shared.answer"),
			},
		}},
	}
	parent := dsl.GraphDefinition{
		Version:      dsl.GraphDefinitionVersion,
		Name:         "parent",
		StateModules: []dsl.StateModuleRef{{Name: moduleName, Version: "1"}},
		EntryPoint:   "child",
		FinishPoint:  "child",
		Nodes: []dsl.GraphNodeSpec{{
			ID: "child", Type: node.NodeTypeSubgraph,
			Config: map[string]any{"graph_ref": "child"},
			State: map[string]dsl.StateBinding{
				"input":  binding("shared.child_payload"),
				"output": binding("shared.child_result"),
			},
		}},
	}
	ctx := &registry.BuildContext{GraphResolver: func(graphRef string) (dsl.GraphDefinition, error) {
		if graphRef != "child" {
			return dsl.GraphDefinition{}, errors.New("unknown graph")
		}
		return child, nil
	}}
	workflow, err := BuildGraph(reg, parent, ctx)
	if err != nil {
		t.Fatalf("BuildGraph(): %v", err)
	}
	result, err := workflow.Run(context.Background(), state.FromShared(map[string]any{
		"child_payload": map[string]any{"value": "isolated"},
	}))
	if err != nil {
		t.Fatalf("Run(): %v", err)
	}
	if _, ok := state.ReadPath(result, "shared.answer"); ok {
		t.Fatal("child shared.answer leaked into the parent root")
	}
	raw, ok := state.ReadPath(result, "shared.child_result")
	if !ok {
		t.Fatal("subgraph output binding is missing")
	}
	exported, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("subgraph output type = %T", raw)
	}
	shared, ok := exported[state.SectionShared].(map[string]any)
	if !ok || shared["answer"] != "isolated:child" {
		t.Fatalf("subgraph shared output = %#v", exported[state.SectionShared])
	}
}

func runBoundGraph(
	t *testing.T,
	def dsl.GraphDefinition,
	initial *state.State,
	models map[string]llms.Model,
	tools map[string]core.Tool,
) *state.State {
	t.Helper()
	reg := builtin.NewDefaultRegistry()
	graph, err := BuildGraph(reg, def, &registry.BuildContext{})
	if err != nil {
		t.Fatalf("BuildGraph(): %v", err)
	}
	ctx := core.WithModels(context.Background(), models)
	if tools != nil {
		ctx = core.WithTools(ctx, tools)
	}
	result, err := graph.Run(ctx, initial)
	if err != nil {
		t.Fatalf("Run(): %v", err)
	}
	return result
}

func protocolModuleRefs() []dsl.StateModuleRef {
	return []dsl.StateModuleRef{{Name: builtin.ProtocolsModuleName, Version: builtin.ProtocolsModuleVersion}}
}

func binding(path string) dsl.StateBinding {
	return dsl.StateBinding{Path: path}
}

func contentResponse(content string) *llms.ContentResponse {
	return &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: content}}}
}

type graphScriptedModel struct {
	mu        sync.Mutex
	responses []*llms.ContentResponse
	calls     [][]llms.MessageContent
}

type graphCopyNode struct {
	core.NodeInfo
	input  state.Path
	output state.Path
}

func (n *graphCopyNode) Execute(_ core.Context, access *state.Access) error {
	value, err := state.Get(access, state.NewRef[string](n.input))
	if err != nil {
		return err
	}
	return state.Replace(access, state.NewRef[string](n.output), value+":child")
}

func (m *graphScriptedModel) GenerateContent(_ context.Context, messages []llms.MessageContent, _ ...llms.CallOption) (*llms.ContentResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.responses) == 0 {
		return nil, errors.New("scripted model exhausted")
	}
	m.calls = append(m.calls, cloneGraphMessages(messages))
	response := m.responses[0]
	m.responses = m.responses[1:]
	return response, nil
}

func (m *graphScriptedModel) Call(context.Context, string, ...llms.CallOption) (string, error) {
	return "", errors.New("scripted model Call is not supported")
}

func (m *graphScriptedModel) Calls() [][]llms.MessageContent {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([][]llms.MessageContent, len(m.calls))
	for index, messages := range m.calls {
		result[index] = cloneGraphMessages(messages)
	}
	return result
}

func cloneGraphMessages(messages []llms.MessageContent) []llms.MessageContent {
	result := make([]llms.MessageContent, len(messages))
	for index, message := range messages {
		result[index] = llms.MessageContent{Role: message.Role, Parts: append([]llms.ContentPart(nil), message.Parts...)}
	}
	return result
}

func firstHumanText(calls [][]llms.MessageContent) string {
	if len(calls) == 0 {
		return ""
	}
	for _, message := range calls[0] {
		if message.Role != llms.ChatMessageTypeHuman {
			continue
		}
		for _, part := range message.Parts {
			if text, ok := part.(llms.TextContent); ok {
				return strings.TrimSpace(text.Text)
			}
		}
	}
	return ""
}

func graphMessageText(message llms.MessageContent) string {
	for _, part := range message.Parts {
		if text, ok := part.(llms.TextContent); ok {
			return strings.TrimSpace(text.Text)
		}
	}
	return ""
}
