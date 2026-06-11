package node

import (
	"context"
	"errors"
	"testing"

	"weaveflow/core"
	"weaveflow/state"
	"weaveflow/state/accessors"

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

type scriptedModel struct {
	responses []*llms.ContentResponse
	calls     int
}

func (m *scriptedModel) GenerateContent(context.Context, []llms.MessageContent, ...llms.CallOption) (*llms.ContentResponse, error) {
	if m.calls >= len(m.responses) {
		return nil, errors.New("scripted model exhausted")
	}
	response := m.responses[m.calls]
	m.calls++
	return response, nil
}

func (m *scriptedModel) Call(context.Context, string, ...llms.CallOption) (string, error) {
	return "", errors.New("scripted model Call is not supported")
}
