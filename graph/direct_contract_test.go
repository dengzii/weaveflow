package graph

import (
	"context"
	"testing"

	"github.com/dengzii/weaveflow/core"
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

	workflow := NewGraph()
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
	runner := NewGraphRunner(
		workflow,
		fruntime.NewFileExecutionStore(dir),
		fruntime.NewFileCheckpointStore(dir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)
	if runner.ContractValidation != core.ContractValidationStrict {
		t.Fatalf("contract validation = %q, want strict", runner.ContractValidation)
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
