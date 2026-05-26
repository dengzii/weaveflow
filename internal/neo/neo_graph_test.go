package neo

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"weaveflow/core"
	wfstate "weaveflow/state"
	"weaveflow/tools"

	"github.com/tmc/langchaingo/llms"
)

type scriptedNeoModel struct {
	responses []*llms.ContentResponse
	calls     int
}

func (m *scriptedNeoModel) GenerateContent(context.Context, []llms.MessageContent, ...llms.CallOption) (*llms.ContentResponse, error) {
	if m.calls >= len(m.responses) {
		return nil, fmt.Errorf("unexpected model call %d", m.calls+1)
	}
	resp := m.responses[m.calls]
	m.calls++
	return resp, nil
}

func (m *scriptedNeoModel) Call(context.Context, string, ...llms.CallOption) (string, error) {
	return "", nil
}

func TestNewGraphCurrentTimeUsesToolLoop(t *testing.T) {
	t.Parallel()

	graph, err := NewGraph(DefaultConfig())
	if err != nil {
		t.Fatalf("build neo graph: %v", err)
	}

	model := &scriptedNeoModel{
		responses: []*llms.ContentResponse{
			{
				Choices: []*llms.ContentChoice{
					{
						ToolCalls: []llms.ToolCall{
							{
								ID:   "call_time",
								Type: "function",
								FunctionCall: &llms.FunctionCall{
									Name:      "current_time",
									Arguments: "{}",
								},
							},
						},
					},
				},
			},
			{
				Choices: []*llms.ContentChoice{
					{
						Content: "The current time is available from the current_time tool.",
					},
				},
			},
		},
	}

	ctx := core.WithServices(context.Background(), &core.Services{
		Model: model,
		Tools: map[string]tools.Tool{
			"current_time": tools.NewCurrentTime(),
		},
	})

	state := NewInitialState("现在几点", nil)
	state, err = graph.Run(ctx, state)
	if err != nil {
		t.Fatalf("run neo graph: %v", err)
	}

	if model.calls != 2 {
		t.Fatalf("expected two llm calls for tool loop, got %d", model.calls)
	}

	orchestration := state.Get(wfstate.StateKeyOrchestration)
	if orchestration == nil {
		t.Fatal("expected orchestration state")
	}
	if got := orchestration["direct_answer"]; got != "" {
		t.Fatalf("expected router to avoid direct_answer for time requests, got %#v", got)
	}

	if got := state.Conversation(stateScope).FinalAnswer(); got != "The current time is available from the current_time tool." {
		t.Fatalf("unexpected final answer: %#v", got)
	}
}

func TestNewGraphClarificationPausesAtClarificationNode(t *testing.T) {
	t.Parallel()

	graph, err := NewGraph(DefaultConfig())
	if err != nil {
		t.Fatalf("build neo graph: %v", err)
	}

	model := &scriptedNeoModel{
		responses: []*llms.ContentResponse{
			{
				Choices: []*llms.ContentChoice{
					{
						Content: `{
  "mode": "planner",
  "use_memory": false,
  "memory_query": "",
  "needs_clarification": true,
  "clarification_question": "Do you want only a diagnosis of the orchestration issues, or should I modify the graph as well?",
  "clarification_options": ["Just diagnose.", "Diagnose and modify the graph."],
  "reasoning": "The requested scope is ambiguous.",
  "target_subgraph": "",
  "direct_answer": ""
}`,
					},
				},
			},
		},
	}

	ctx := core.WithServices(context.Background(), &core.Services{Model: model})

	state := NewInitialState("neo agent 编排不是很好现在, 检查需要优化的点", nil)
	state, err = graph.Run(ctx, state)
	if err == nil {
		t.Fatal("expected graph to pause at clarification node, got no error")
	}
	if !strings.Contains(err.Error(), "Clarification_") {
		t.Fatalf("expected interrupt at Clarification_ node, got: %v", err)
	}

	if model.calls != 1 {
		t.Fatalf("expected only router LLM call before pause, got %d", model.calls)
	}

	orchestration := state.Get(wfstate.StateKeyOrchestration)
	if orchestration == nil {
		t.Fatal("expected orchestration state to be populated")
	}
	if got, _ := orchestration["needs_clarification"].(bool); !got {
		t.Fatalf("expected orchestration.needs_clarification=true, got %#v", orchestration["needs_clarification"])
	}
	question, _ := orchestration["clarification_question"].(string)
	if question == "" || !strings.Contains(question, "diagnosis") {
		t.Fatalf("expected clarification_question to carry router question, got %#v", question)
	}
	options := optionsFromOrchestration(orchestration)
	if len(options) < 2 {
		t.Fatalf("expected clarification_options to be populated, got %#v", orchestration["clarification_options"])
	}
}

func TestNewGraphPlannerUsesMemoryBeforePlanning(t *testing.T) {
	t.Parallel()

	graph, err := NewGraph(DefaultConfig())
	if err != nil {
		t.Fatalf("build neo graph: %v", err)
	}

	model := &scriptedNeoModel{
		responses: []*llms.ContentResponse{
			contentResponse(`{
  "mode": "planner",
  "use_memory": true,
  "memory_query": "prior deployment notes",
  "needs_clarification": false,
  "clarification_question": "",
  "clarification_options": [],
  "reasoning": "Prior context can help planning.",
  "target_subgraph": "",
  "direct_answer": ""
}`),
			contentResponse(`{
  "objective": "Use memory before planning",
  "status": "planned",
  "summary": "One step.",
  "replan_reason": "",
  "plan": [{
    "id": "step_1",
    "title": "Answer",
    "description": "Answer from available context.",
    "status": "ready",
    "kind": "decision",
    "node_type": "llm",
    "depends_on": [],
    "inputs": ["memory.recalled"],
    "outputs": ["answer"],
    "acceptance_criteria": [],
    "parallelizable": false
  }]
}`),
			contentResponse("Memory-aware answer."),
			contentResponse("Final memory-aware answer."),
		},
	}

	ctx := core.WithServices(context.Background(), &core.Services{Model: model})
	state := NewInitialState("Use prior deployment notes", nil)
	state, err = graph.Run(ctx, state)
	if err != nil {
		t.Fatalf("run neo graph: %v", err)
	}

	memoryState := state.Get(wfstate.StateKeyMemory)
	if memoryState == nil {
		t.Fatal("expected memory recall state")
	}
	stats := wfstate.State(nil)
	switch typed := memoryState["stats"].(type) {
	case wfstate.State:
		stats = typed
	case map[string]any:
		stats = typed
	}
	if stats == nil {
		t.Fatalf("expected memory stats, got %#v", memoryState["stats"])
	}
	if requested, _ := stats["requested"].(bool); !requested {
		t.Fatalf("expected memory recall to be requested, got %#v", stats)
	}
}

func TestNewGraphValidationStepRoutesDirectlyToVerifier(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.Mode = "planner"
	graph, err := NewGraph(cfg)
	if err != nil {
		t.Fatalf("build neo graph: %v", err)
	}

	model := &scriptedNeoModel{
		responses: []*llms.ContentResponse{
			contentResponse(`{
  "mode": "planner",
  "use_memory": false,
  "memory_query": "",
  "needs_clarification": false,
  "clarification_question": "",
  "clarification_options": [],
  "reasoning": "Validation is required.",
  "target_subgraph": "",
  "direct_answer": ""
}`),
			contentResponse(`{
  "objective": "Validate the prepared answer",
  "status": "planned",
  "summary": "Validate first.",
  "replan_reason": "",
  "plan": [{
    "id": "step_1",
    "title": "Validate answer",
    "description": "Check that existing evidence satisfies the request.",
    "status": "ready",
    "kind": "validation",
    "node_type": "verifier",
    "depends_on": [],
    "inputs": ["observations"],
    "outputs": ["verification"],
    "acceptance_criteria": ["The answer is validated."],
    "parallelizable": false
  }]
}`),
			contentResponse(`{"status":"pass","issues":[],"summary":"Validation passed.","suggestion":"continue"}`),
			contentResponse("Final answer after validation."),
		},
	}

	ctx := core.WithServices(context.Background(), &core.Services{Model: model})
	state := NewInitialState("Validate the prepared answer", nil)
	state, err = graph.Run(ctx, state)
	if err != nil {
		t.Fatalf("run neo graph: %v", err)
	}

	if model.calls != 4 {
		t.Fatalf("expected router, planner, verifier, finalizer calls only; got %d", model.calls)
	}
	verification := state.Get(wfstate.StateKeyVerification)
	if got := verification["summary"]; got != "Validation passed." {
		t.Fatalf("expected validation verifier result, got %#v", verification)
	}
}

func TestNewGraphHumanInputStepPausesAtHumanMessage(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.Mode = "planner"
	graph, err := NewGraph(cfg)
	if err != nil {
		t.Fatalf("build neo graph: %v", err)
	}

	model := &scriptedNeoModel{
		responses: []*llms.ContentResponse{
			contentResponse(`{
  "mode": "planner",
  "use_memory": false,
  "memory_query": "",
  "needs_clarification": false,
  "clarification_question": "",
  "clarification_options": [],
  "reasoning": "Human input is needed.",
  "target_subgraph": "",
  "direct_answer": ""
}`),
			contentResponse(`{
  "objective": "Ask for environment",
  "status": "planned",
  "summary": "Need environment input.",
  "replan_reason": "",
  "plan": [{
    "id": "step_1",
    "title": "Collect environment",
    "description": "Ask the user for the target environment.",
    "status": "ready",
    "kind": "human_input",
    "node_type": "human_message",
    "depends_on": [],
    "inputs": ["request.input"],
    "outputs": ["environment"],
    "acceptance_criteria": ["The target environment is known."],
    "parallelizable": false
  }]
}`),
		},
	}

	ctx := core.WithServices(context.Background(), &core.Services{Model: model})
	_, err = graph.Run(ctx, NewInitialState("Deploy it", nil))
	if err == nil {
		t.Fatal("expected graph to pause for human input")
	}
	if !strings.Contains(err.Error(), "HumanMessage_") {
		t.Fatalf("expected interrupt at HumanMessage node, got: %v", err)
	}
}

func TestNewGraphPlannerNeedsClarificationPausesAtClarificationNode(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.Mode = "planner"
	graph, err := NewGraph(cfg)
	if err != nil {
		t.Fatalf("build neo graph: %v", err)
	}

	model := &scriptedNeoModel{
		responses: []*llms.ContentResponse{
			contentResponse(`{
  "mode": "planner",
  "use_memory": false,
  "memory_query": "",
  "needs_clarification": false,
  "clarification_question": "",
  "clarification_options": [],
  "reasoning": "Route to planner.",
  "target_subgraph": "",
  "direct_answer": ""
}`),
			contentResponse(`{
  "objective": "Deploy it",
  "status": "needs_clarification",
  "summary": "Which environment should be deployed to?",
  "replan_reason": "",
  "plan": []
}`),
		},
	}

	ctx := core.WithServices(context.Background(), &core.Services{Model: model})
	state := NewInitialState("Deploy it", nil)
	state, err = graph.Run(ctx, state)
	if err == nil {
		t.Fatal("expected graph to pause for clarification")
	}
	if !strings.Contains(err.Error(), "Clarification_") {
		t.Fatalf("expected interrupt at Clarification node, got: %v", err)
	}
	planner := state.Get(wfstate.StateKeyPlanner)
	if got := planner["current_step_id"]; got != "" {
		t.Fatalf("expected empty current_step_id for empty clarification plan, got %#v", got)
	}
}

func optionsFromOrchestration(orchestration wfstate.State) []string {
	switch typed := orchestration["clarification_options"].(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, raw := range typed {
			if s, ok := raw.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
