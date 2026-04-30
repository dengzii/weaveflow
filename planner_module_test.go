package weaveflow

import (
	"context"
	"testing"
	"weaveflow/dsl"
	fruntime "weaveflow/runtime"

	"github.com/tmc/langchaingo/llms"
)

type stubPlannerModel struct {
	response string
	err      error
}

func (m stubPlannerModel) GenerateContent(context.Context, []llms.MessageContent, ...llms.CallOption) (*llms.ContentResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &llms.ContentResponse{
		Choices: []*llms.ContentChoice{
			{
				Content: m.response,
			},
		},
	}, nil
}

func (m stubPlannerModel) Call(context.Context, string, ...llms.CallOption) (string, error) {
	return m.response, m.err
}

func TestRegisterPlannerModuleBuildsAndRunsPlannerNode(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	RegisterPlannerModule(registry)

	if _, ok := registry.StateFields[fruntime.StateKeyPlanner]; !ok {
		t.Fatalf("expected planner state field to be registered")
	}

	def := GraphDefinition{
		EntryPoint:  "plan",
		FinishPoint: "plan",
		Nodes: []GraphNodeSpec{
			{
				ID:   "plan",
				Type: "planner",
				Config: map[string]any{
					"max_steps":       2,
					"step_kind_hints": []string{"search", "synthesize"},
				},
			},
		},
	}

	graph, err := registry.BuildGraph(def, &BuildContext{})
	if err != nil {
		t.Fatalf("build planner graph: %v", err)
	}

	svc := &fruntime.Services{
		Model: stubPlannerModel{
			response: `{
  "objective": "Investigate browser automation tradeoffs",
  "status": "planned",
  "summary": "Gather sources first, then synthesize.",
  "plan": [
    {
      "id": "step_search",
      "title": "Search for relevant sources",
      "description": "Collect current sources about browser automation approaches.",
      "status": "pending",
      "kind": "search",
      "depends_on": [],
      "acceptance_criteria": ["At least 3 credible sources collected"],
      "outputs": ["source_candidates"]
    },
    {
      "id": "step_synthesize",
      "title": "Synthesize the findings",
      "description": "Compare the tradeoffs in a concise summary.",
      "status": "pending",
      "kind": "synthesize",
      "depends_on": ["step_search"],
      "acceptance_criteria": ["Summary covers strengths and weaknesses"],
      "outputs": ["draft_summary"]
    }
  ]
}`,
		},
	}
	ctx := fruntime.WithServices(context.Background(), svc)

	initialState := State{}
	planner := initialState.Ensure(fruntime.StateKeyPlanner)
	planner["objective"] = "Investigate browser automation tradeoffs"

	state, err := graph.Run(ctx, initialState)
	if err != nil {
		t.Fatalf("run planner graph: %v", err)
	}

	finalPlanner := state.Get(fruntime.StateKeyPlanner)
	if finalPlanner == nil {
		t.Fatal("expected planner state to be present after run")
	}
	if got := finalPlanner["status"]; got != "planned" {
		t.Fatalf("expected planner status planned, got %#v", got)
	}
	if got := finalPlanner["current_step_id"]; got != "step_search" {
		t.Fatalf("expected current step to point at first plan item, got %#v", got)
	}
	plan, ok := finalPlanner["plan"].([]map[string]any)
	if !ok || len(plan) != 2 {
		t.Fatalf("expected planner to write a two-step plan, got %#v", finalPlanner["plan"])
	}
	if plan[0]["kind"] != "search" || plan[1]["kind"] != "synthesize" {
		t.Fatalf("unexpected plan kinds: %#v", plan)
	}
}

func TestPlannerStatusEqualsConditionMatchesPlannerState(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	RegisterPlannerModule(registry)

	condition, err := registry.ResolveCondition(GraphConditionSpec{
		Type: "planner_status_equals",
		Config: map[string]any{
			"status": "planned",
		},
	})
	if err != nil {
		t.Fatalf("resolve planner status condition: %v", err)
	}

	state := State{}
	state.Ensure(fruntime.StateKeyPlanner)["status"] = "planned"

	if !condition.Match(context.Background(), state) {
		t.Fatal("expected planner_status_equals condition to match planner state")
	}
}

func TestResolvePlannerStateContractUsesConfigPaths(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	RegisterPlannerModule(registry)

	contract, err := registry.ResolveNodeStateContract(GraphNodeSpec{
		ID:   "plan",
		Type: "planner",
		Config: map[string]any{
			"planner_state_path": "execution.plan",
			"objective_path":     "request.objective",
			"context_paths":      []string{"request.constraints", "research.notes"},
		},
	})
	if err != nil {
		t.Fatalf("resolve planner state contract: %v", err)
	}

	if len(contract.Fields) != 4 {
		t.Fatalf("expected 4 contract fields, got %#v", contract.Fields)
	}
	if contract.Fields[0].Path != "request.objective" || contract.Fields[0].Mode != dsl.StateAccessRead {
		t.Fatalf("unexpected objective contract field: %#v", contract.Fields[0])
	}
	if contract.Fields[3].Path != "execution.plan" || contract.Fields[3].Mode != dsl.StateAccessWrite {
		t.Fatalf("unexpected planner output contract field: %#v", contract.Fields[3])
	}
}
