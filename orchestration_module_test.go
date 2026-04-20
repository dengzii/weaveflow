package weaveflow

import (
	"context"
	"testing"
	"weaveflow/dsl"
	fruntime "weaveflow/runtime"

	"github.com/tmc/langchaingo/llms"
)

type stubOrchestrationModel struct {
	response string
	err      error
}

func (m stubOrchestrationModel) GenerateContent(context.Context, []llms.MessageContent, ...llms.CallOption) (*llms.ContentResponse, error) {
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

func (m stubOrchestrationModel) Call(context.Context, string, ...llms.CallOption) (string, error) {
	return m.response, m.err
}

func TestOrchestrationRouterRegisteredInDefaultRegistry(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	if _, ok := registry.NodeTypes["orchestration_router"]; !ok {
		t.Fatal("expected orchestration_router node type to be registered")
	}
	if _, ok := registry.StateFields[fruntime.StateKeyOrchestration]; !ok {
		t.Fatal("expected orchestration state field to be registered")
	}
}

func TestOrchestrationRouterBuildsAndWritesState(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()

	def := GraphDefinition{
		EntryPoint:  "route",
		FinishPoint: "route",
		Nodes: []GraphNodeSpec{
			{
				ID:   "route",
				Type: "orchestration_router",
				Config: map[string]any{
					"input_path":               "request.text",
					"orchestration_state_path": "analysis.orchestration",
					"context_paths":            []string{"request.meta"},
					"available_modes":          []string{"direct", "planner", "supervisor"},
				},
			},
		},
	}

	graph, err := registry.BuildGraph(def, &BuildContext{
		Model: stubOrchestrationModel{
			response: `{
  "mode": "planner",
  "use_memory": true,
  "memory_query": "memory retrieval policy for planner vs supervisor",
  "needs_clarification": true,
  "clarification_question": "Do you need only node design, or also concrete graph wiring and memory policy?",
  "reasoning": "The request asks for orchestration policy and architecture, which benefits from planning and prior context.",
  "target_subgraph": "planner_flow"
}`,
		},
	})
	if err != nil {
		t.Fatalf("build orchestration graph: %v", err)
	}

	state := State{
		"request": map[string]any{
			"text": "Should this request use memory and planner or supervisor?",
			"meta": map[string]any{
				"source": "test",
			},
		},
	}
	state, err = graph.Run(context.Background(), state)
	if err != nil {
		t.Fatalf("run orchestration graph: %v", err)
	}

	value, ok := fruntime.ResolveStatePath(state, "analysis.orchestration")
	if !ok {
		t.Fatal("expected nested orchestration state to be written")
	}
	orchestrationState, ok := value.(map[string]any)
	if !ok {
		if typed, typedOK := value.(State); typedOK {
			orchestrationState = typed
		} else {
			t.Fatalf("expected orchestration state map, got %T", value)
		}
	}

	if got := orchestrationState["mode"]; got != "planner" {
		t.Fatalf("expected mode planner, got %#v", got)
	}
	if got := orchestrationState["use_memory"]; got != true {
		t.Fatalf("expected use_memory=true, got %#v", got)
	}
	if got := orchestrationState["needs_clarification"]; got != true {
		t.Fatalf("expected needs_clarification=true, got %#v", got)
	}
	if got := orchestrationState["target_subgraph"]; got != "planner_flow" {
		t.Fatalf("expected target_subgraph planner_flow, got %#v", got)
	}
}

func TestResolveOrchestrationRouterStateContractUsesConfiguredPaths(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	contract, err := registry.ResolveNodeStateContract(GraphNodeSpec{
		ID:   "route",
		Type: "orchestration_router",
		Config: map[string]any{
			"input_path":               "request.input",
			"orchestration_state_path": "analysis.orchestration",
			"context_paths":            []string{"request.constraints", "memory.summary"},
		},
	})
	if err != nil {
		t.Fatalf("resolve orchestration state contract: %v", err)
	}

	if len(contract.Fields) != 4 {
		t.Fatalf("expected 4 contract fields, got %#v", contract.Fields)
	}
	if contract.Fields[0].Path != "memory.summary" || contract.Fields[0].Mode != dsl.StateAccessRead {
		t.Fatalf("unexpected contract field[0]: %#v", contract.Fields[0])
	}
	if contract.Fields[1].Path != "request.constraints" || contract.Fields[1].Mode != dsl.StateAccessRead {
		t.Fatalf("unexpected contract field[1]: %#v", contract.Fields[1])
	}
	if contract.Fields[2].Path != "request.input" || contract.Fields[2].Mode != dsl.StateAccessRead {
		t.Fatalf("unexpected contract field[2]: %#v", contract.Fields[2])
	}
	if contract.Fields[3].Path != "analysis.orchestration" || contract.Fields[3].Mode != dsl.StateAccessWrite {
		t.Fatalf("unexpected contract field[3]: %#v", contract.Fields[3])
	}
}
