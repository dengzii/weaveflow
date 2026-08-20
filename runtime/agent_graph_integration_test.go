package runtime_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/dengzii/weaveflow/core"
	wfgraph "github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/llms"
	"github.com/dengzii/weaveflow/node/agents/agent"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

func TestGraphAgentPersistsInvocationStepsAndResumesWithoutRepeatingTool(t *testing.T) {
	workflow := wfgraph.NewGraph(nil)
	agentNode := agent.NewNode(core.WithID("agent"))
	agentNode.ToolIDs = []string{"lookup"}
	if err := workflow.AddNode(agentNode); err != nil {
		t.Fatalf("AddNode() error = %v", err)
	}
	if err := workflow.SetEntryPoint(agentNode.ID()); err != nil {
		t.Fatalf("SetEntryPoint() error = %v", err)
	}
	if err := workflow.AddEdge(agentNode.ID(), wfgraph.EndNodeRef); err != nil {
		t.Fatalf("AddEdge() error = %v", err)
	}

	store := runtime.NewMemoryRuntimeStore()
	runner, err := wfgraph.NewGraphRunner(
		workflow,
		store,
		store,
		state.NewJSONStateCodec(""),
		store,
		runtime.WithRuntimeTransactionStore(store),
		runtime.WithGraphMetadata("graph", "v1", "hash", "snapshot", "session"),
	)
	if err != nil {
		t.Fatalf("NewGraphRunner() error = %v", err)
	}

	model := &graphAgentScriptedModel{responses: []graphAgentModelResponse{
		{response: &llms.ModelResponse{Choices: []*llms.ModelChoice{{ToolCalls: []llms.ToolCall{graphAgentToolCall("lookup-call")}}}}},
		{err: errors.New("provider temporarily unavailable")},
		{response: &llms.ModelResponse{Choices: []*llms.ModelChoice{{Content: "finished"}}}},
	}}
	var toolCalls int
	tool := core.NewTool(&llms.FunctionDefinition{Name: "lookup"}, func(_ context.Context, call llms.ToolCall) (llms.ToolResult, error) {
		toolCalls++
		return llms.ToolResult{ToolCallID: call.ID, Content: "ready"}, nil
	})
	ctx := core.WithModel(context.Background(), model)
	ctx = core.WithTools(ctx, map[string]core.Tool{"lookup": tool})
	initial := state.FromShared(map[string]any{"request": map[string]any{"input": "check status"}})

	failedRun, _, err := runner.Start(ctx, initial)
	if err == nil || failedRun.Status != runtime.RunStatusFailed {
		t.Fatalf("first Start() = run=%#v err=%v, want failed run", failedRun, err)
	}
	steps, err := store.ListSteps(context.Background(), failedRun.RunID)
	if err != nil {
		t.Fatalf("ListSteps() error = %v", err)
	}
	if len(steps) < 3 {
		t.Fatalf("steps = %#v, want agent node and invocation steps", steps)
	}
	checkpoints, err := store.List(context.Background(), failedRun.RunID)
	if err != nil {
		t.Fatalf("ListCheckpoints() error = %v", err)
	}
	foundAgentCheckpoint := false
	for _, checkpoint := range checkpoints {
		if checkpoint.Stage == runtime.CheckpointAgent {
			foundAgentCheckpoint = true
		}
	}
	if !foundAgentCheckpoint {
		t.Fatalf("checkpoints = %#v, want agent invocation checkpoint", checkpoints)
	}
	if toolCalls != 1 {
		t.Fatalf("tool calls before resume = %d, want 1", toolCalls)
	}
	failedRun.Status = runtime.RunStatusPaused
	failedRun.ErrorCode = ""
	failedRun.ErrorMessage = ""
	failedRun.FinishedAt = nil
	if _, err := store.CompareAndSwapRun(context.Background(), failedRun.Revision, failedRun); err != nil {
		t.Fatalf("mark run paused for resume test: %v", err)
	}

	completedRun, _, err := runner.Resume(ctx, failedRun.RunID, nil)
	if err != nil {
		resumed, _ := store.GetRun(context.Background(), failedRun.RunID)
		resumedSteps, _ := store.ListSteps(context.Background(), failedRun.RunID)
		t.Logf("resume stored run=%#v steps=%#v", resumed, resumedSteps)
		t.Fatalf("Resume() error = %v", err)
	}
	if completedRun.Status != runtime.RunStatusCompleted || toolCalls != 1 {
		t.Fatalf("resumed run = %#v, tool calls = %d; want completed and one tool call", completedRun, toolCalls)
	}
}

func TestGraphAgentBudgetRejectsExcessToolCallsBeforeExecution(t *testing.T) {
	workflow := wfgraph.NewGraph(nil)
	agentNode := agent.NewNode(core.WithID("agent"))
	agentNode.ToolIDs = []string{"lookup"}
	agentNode.MaxToolCalls = 1
	if err := workflow.AddNode(agentNode); err != nil {
		t.Fatalf("AddNode() error = %v", err)
	}
	if err := workflow.SetEntryPoint(agentNode.ID()); err != nil {
		t.Fatalf("SetEntryPoint() error = %v", err)
	}
	if err := workflow.AddEdge(agentNode.ID(), wfgraph.EndNodeRef); err != nil {
		t.Fatalf("AddEdge() error = %v", err)
	}
	store := runtime.NewMemoryRuntimeStore()
	runner, err := wfgraph.NewGraphRunner(workflow, store, store, state.NewJSONStateCodec(""), store, runtime.WithRuntimeTransactionStore(store))
	if err != nil {
		t.Fatalf("NewGraphRunner() error = %v", err)
	}
	model := &graphAgentScriptedModel{responses: []graphAgentModelResponse{{response: &llms.ModelResponse{Choices: []*llms.ModelChoice{{ToolCalls: []llms.ToolCall{
		graphAgentToolCall("first"), graphAgentToolCall("second"),
	}}}}}}}
	var toolCalls int
	tool := core.NewTool(&llms.FunctionDefinition{Name: "lookup"}, func(_ context.Context, call llms.ToolCall) (llms.ToolResult, error) {
		toolCalls++
		return llms.ToolResult{ToolCallID: call.ID, Content: "ready"}, nil
	})
	ctx := core.WithModel(context.Background(), model)
	ctx = core.WithTools(ctx, map[string]core.Tool{"lookup": tool})
	run, _, err := runner.Start(ctx, state.FromShared(map[string]any{"request": map[string]any{"input": "check status"}}))
	if err == nil || run.Status != runtime.RunStatusFailed {
		t.Fatalf("Start() = run=%#v err=%v, want budget failure", run, err)
	}
	if toolCalls != 0 {
		t.Fatalf("tool calls = %d, want zero after budget rejection", toolCalls)
	}
	if run.ErrorCode != string(core.ErrorResourceExhausted) {
		t.Fatalf("run error code = %q, want %q", run.ErrorCode, core.ErrorResourceExhausted)
	}
}

type graphAgentModelResponse struct {
	response *llms.ModelResponse
	err      error
}

type graphAgentScriptedModel struct {
	mu        sync.Mutex
	responses []graphAgentModelResponse
}

func (model *graphAgentScriptedModel) Generate(_ context.Context, _ llms.ModelRequest) (*llms.ModelResponse, error) {
	model.mu.Lock()
	defer model.mu.Unlock()
	if len(model.responses) == 0 {
		return nil, errors.New("scripted model exhausted")
	}
	item := model.responses[0]
	model.responses = model.responses[1:]
	return item.response, item.err
}

func graphAgentToolCall(id string) llms.ToolCall {
	return llms.ToolCall{ID: id, Type: "function", FunctionCall: &llms.FunctionCall{Name: "lookup", Arguments: []byte(`{}`)}}
}
