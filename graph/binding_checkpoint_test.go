package graph

import (
	"context"
	"testing"

	"github.com/dengzii/weaveflow/builtin"
	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/node"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"

	"github.com/tmc/langchaingo/llms"
)

func TestGraphV2CheckpointResumePreservesBoundConversation(t *testing.T) {
	t.Parallel()
	conversationPath := "scopes.writer.conversation"
	definition := dsl.GraphDefinition{
		Version:      dsl.GraphDefinitionVersion,
		StateModules: protocolModuleRefs(),
		EntryPoint:   "input",
		FinishPoint:  "llm",
		Nodes: []dsl.GraphNodeSpec{
			{
				ID: "input", Type: node.NodeTypeConversationInput,
				State: map[string]dsl.StateBinding{
					"input":        binding("shared.request.input"),
					"conversation": binding(conversationPath),
				},
			},
			{
				ID: "llm", Type: node.NodeTypeLLM,
				Config: map[string]any{"model_id": "writer"},
				State: map[string]dsl.StateBinding{
					"conversation": binding(conversationPath),
					"output":       binding("shared.final.answer"),
				},
			},
		},
		Edges: []dsl.GraphEdgeSpec{{From: "input", To: "llm"}},
	}
	workflow, err := BuildGraph(builtin.NewDefaultRegistry(), definition, nil)
	if err != nil {
		t.Fatalf("BuildGraph(): %v", err)
	}
	dir := t.TempDir()
	runner := NewGraphRunner(
		workflow,
		fruntime.NewFileExecutionStore(dir),
		fruntime.NewFileCheckpointStore(dir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)
	runner.Breakpoints = []fruntime.Breakpoint{{
		ID: "after-input", NodeID: "input", Stage: string(fruntime.CheckpointAfterNode), Enabled: true,
	}}
	model := &graphScriptedModel{responses: []*llms.ContentResponse{contentResponse("final answer")}}
	ctx := core.WithModels(context.Background(), map[string]llms.Model{"writer": model})
	run, _, err := runner.Start(ctx, state.FromShared(map[string]any{
		"request": map[string]any{"input": "draft the answer"},
	}))
	if err != nil {
		t.Fatalf("runner start: %v", err)
	}
	if run.Status != fruntime.RunStatusPaused {
		t.Fatalf("run status = %q, want paused", run.Status)
	}
	restored, err := runner.LoadCheckpointState(context.Background(), run.LastCheckpointID)
	if err != nil {
		t.Fatalf("load checkpoint: %v", err)
	}
	conversation, err := conversationcap.Bind(state.NewAccess(restored.Business), state.Scope("writer", "conversation"))
	if err != nil {
		t.Fatalf("bind restored conversation: %v", err)
	}
	messages := conversation.Messages()
	if len(messages) != 1 || messages[0].Role != llms.ChatMessageTypeHuman || graphMessageText(messages[0]) != "draft the answer" {
		t.Fatalf("restored messages = %#v", messages)
	}

	resumedRun, result, err := runner.Resume(ctx, run.RunID, nil)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumedRun.Status != fruntime.RunStatusCompleted {
		t.Fatalf("resumed status = %q, want completed", resumedRun.Status)
	}
	answer, _ := state.ReadPath(result, "shared.final.answer")
	if answer != "final answer" {
		t.Fatalf("answer = %#v", answer)
	}
	conversation, _ = conversationcap.Bind(state.NewAccess(result), state.Scope("writer", "conversation"))
	if messages = conversation.Messages(); len(messages) != 2 || messages[1].Role != llms.ChatMessageTypeAI || graphMessageText(messages[1]) != "final answer" {
		t.Fatalf("resumed messages = %#v", messages)
	}
}
