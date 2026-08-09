package graph

import (
	"context"
	"testing"

	"github.com/dengzii/weaveflow/builtin"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/node"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"

	"github.com/tmc/langchaingo/llms"
)

func TestDirectBuiltinGraphResolvesStrictStateContracts(t *testing.T) {
	t.Parallel()
	conversationPath := state.Scope("direct", "conversation")
	input := node.NewUserInputNode(node.WithID("input"))
	message := node.NewConversationMessageNode(node.WithID("message"))
	message.InputPath = input.ValuePath
	message.ConversationPath = conversationPath
	llm := node.NewLLMTurnNode(node.WithID("llm"))
	llm.ModelID = "direct"
	llm.ConversationPath = conversationPath
	llm.OutputPath = state.Shared("final", "answer")

	workflow := NewGraph(builtin.NewDefaultRegistry())
	if err := workflow.AddNode(input); err != nil {
		t.Fatalf("add input: %v", err)
	}
	if err := workflow.AddNode(message); err != nil {
		t.Fatalf("add message: %v", err)
	}
	if err := workflow.AddNode(llm); err != nil {
		t.Fatalf("add llm: %v", err)
	}
	if err := workflow.SetEntryPoint(input.ID()); err != nil {
		t.Fatalf("set entry point: %v", err)
	}
	if err := workflow.SetFinishPoint(llm.ID()); err != nil {
		t.Fatalf("set finish point: %v", err)
	}
	if err := workflow.AddEdge(input.ID(), message.ID()); err != nil {
		t.Fatalf("add edge: %v", err)
	}
	if err := workflow.AddEdge(message.ID(), llm.ID()); err != nil {
		t.Fatalf("add edge: %v", err)
	}
	if len(workflow.nodeContracts[input.ID()].Fields) == 0 || len(workflow.nodeContracts[message.ID()].Fields) == 0 || len(workflow.nodeContracts[llm.ID()].Fields) == 0 {
		t.Fatalf("resolved contracts = %#v", workflow.nodeContracts)
	}

	dir := t.TempDir()
	runner := mustNewGraphRunner(t,
		workflow,
		fruntime.NewFileExecutionStore(dir),
		fruntime.NewFileCheckpointStore(dir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)
	if runner.ContractValidation() != core.ContractValidationStrict {
		t.Fatalf("contract validation = %q, want strict", runner.ContractValidation())
	}
	model := &graphScriptedModel{responses: []*llms.ContentResponse{contentResponse("done")}}
	run, result, err := runner.Start(core.WithModels(context.Background(), map[string]llms.Model{"direct": model}), state.FromShared(map[string]any{
		"request": map[string]any{"input": "hello"},
	}))
	if err != nil {
		t.Fatalf("runner start: %v", err)
	}
	if run.Status != fruntime.RunStatusCompleted {
		t.Fatalf("run status = %q", run.Status)
	}
	answer, _ := state.ReadPath(result, "shared.final.answer")
	if answer != "done" {
		t.Fatalf("answer = %#v", answer)
	}
}

func TestSetNodeSpecRefreshesBuiltinStateContract(t *testing.T) {
	t.Parallel()

	workflow := NewGraph(builtin.NewDefaultRegistry())
	input := node.NewUserInputNode(node.WithID("input"))
	if err := workflow.AddNode(input); err != nil {
		t.Fatalf("add input: %v", err)
	}
	if err := workflow.SetNodeSpec(dsl.GraphNodeSpec{
		ID:   "input",
		Type: node.NodeTypeUserInput,
		State: map[string]dsl.StateBinding{
			"value": {Path: "shared.first"},
		},
	}); err != nil {
		t.Fatalf("set first node spec: %v", err)
	}
	first := workflow.nodeContracts["input"]
	if err := workflow.SetNodeSpec(dsl.GraphNodeSpec{
		ID:   "input",
		Type: node.NodeTypeUserInput,
		State: map[string]dsl.StateBinding{
			"value": {Path: "shared.second"},
		},
	}); err != nil {
		t.Fatalf("set second node spec: %v", err)
	}
	second := workflow.nodeContracts["input"]
	if len(first.Fields) == 0 || len(second.Fields) == 0 || first.Fields[0].Path.String() == second.Fields[0].Path.String() {
		t.Fatalf("node contract was not refreshed: first=%#v second=%#v", first, second)
	}
	if len(second.Fields) == 0 || second.Fields[0].Path.String() != "shared.second" {
		t.Fatalf("refreshed contract = %#v, want shared.second", second)
	}
	previousSpec := workflow.nodeSpecs["input"]
	if err := workflow.SetNodeSpec(dsl.GraphNodeSpec{
		ID:   "input",
		Type: node.NodeTypeUserInput,
		State: map[string]dsl.StateBinding{
			"unknown": {Path: "shared.invalid"},
		},
	}); err == nil {
		t.Fatal("expected invalid binding error")
	}
	if got := workflow.nodeSpecs["input"]; got.State["value"].Path != previousSpec.State["value"].Path {
		t.Fatalf("node spec was not rolled back: %#v", got)
	}
	rolledBack := workflow.nodeContracts["input"]
	if len(rolledBack.Fields) == 0 || rolledBack.Fields[0].Path.String() != "shared.second" {
		t.Fatalf("node contract was not rolled back: %#v", rolledBack)
	}
}

func TestUserInputEntryProvidesAgentTaskWhenNodeIDIsInput(t *testing.T) {
	t.Parallel()
	definition := dsl.GraphDefinition{
		Version:      dsl.GraphDefinitionVersion,
		StateModules: []dsl.StateModuleRef{{Name: builtin.ProtocolsModuleName, Version: builtin.ProtocolsModuleVersion}},
		EntryPoint:   "input",
		FinishPoint:  "agent",
		Nodes: []dsl.GraphNodeSpec{
			{ID: "input", Type: node.NodeTypeUserInput, State: map[string]dsl.StateBinding{
				"value": {Path: "shared.request.input"}, "pending_input": {Path: "shared.request.pending_input"},
			}},
			{ID: "agent", Type: node.NodeTypeAgent, State: map[string]dsl.StateBinding{
				"task": {Path: "shared.request.input"}, "conversation": {Path: "scopes.agent.conversation"}, "result": {Path: "shared.final.answer"},
			}},
		},
		Edges: []dsl.GraphEdgeSpec{{From: "input", To: "agent"}},
	}

	workflow, err := NewBuilder(builtin.NewDefaultRegistry()).Build(definition, nil)
	if err != nil {
		t.Fatalf("BuildGraph(): %v", err)
	}
	requirements := workflow.InitialStateRequirements()
	if len(requirements.Required) != 0 {
		t.Fatalf("required = %#v, want empty", requirements.Required)
	}
	if len(requirements.ProvidedByUpstream) != 1 {
		t.Fatalf("provided_by_upstream = %#v, want one item", requirements.ProvidedByUpstream)
	}
	provided := requirements.ProvidedByUpstream[0]
	if provided.Path != "shared.request.input" || len(provided.Sources) != 1 || provided.Sources[0] != "input" {
		t.Fatalf("provided_by_upstream = %#v, want source node input", provided)
	}
}
