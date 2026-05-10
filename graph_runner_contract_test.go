package weaveflow

import (
	"context"
	"strings"
	"testing"
	"weaveflow/dsl"
	"weaveflow/nodes"
	fruntime "weaveflow/runtime"
)

type contractProbeNode struct {
	id      string
	spec    dsl.GraphNodeSpec
	mutate  func(State)
	inspect func(State) string
}

func (n contractProbeNode) ID() string          { return n.id }
func (n contractProbeNode) Name() string        { return n.id }
func (n contractProbeNode) Description() string { return "probe state contract runner behavior" }
func (n contractProbeNode) Invoke(ctx context.Context, state State) (State, error) {
	_ = ctx
	if state == nil {
		state = State{}
	}
	if n.inspect != nil {
		state["result"] = n.inspect(state)
	}
	if n.mutate != nil {
		n.mutate(state)
	}
	return state, nil
}

func (n contractProbeNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return n.spec
}

func registerContractProbeNodeType(registry *Registry, contract dsl.StateContract, mutate func(State), inspect func(State) string) {
	registry.RegisterNodeType(NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "contract_probe",
			Description: "Test node for runner state contract execution.",
			ConfigSchema: JSONSchema{
				"type":                 "object",
				"additionalProperties": false,
			},
		},
		ResolveStateContract: func(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
			_ = spec
			return contract.Clone(), nil
		},
		Build: func(ctx *BuildContext, spec dsl.GraphNodeSpec) (nodes.Node[State], error) {
			_ = ctx
			return contractProbeNode{
				id:      spec.ID,
				spec:    spec,
				mutate:  mutate,
				inspect: inspect,
			}, nil
		},
	})
}

func newContractTestRunner(t *testing.T, graph *Graph) *fruntime.GraphRunner {
	t.Helper()

	baseDir := t.TempDir()
	runner := NewGraphRunner(
		graph,
		fruntime.NewFileExecutionStore(baseDir),
		fruntime.NewFileCheckpointStore(baseDir),
		fruntime.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(baseDir),
	)
	runner.ContractValidation = fruntime.ContractValidationStrict
	return runner
}

func TestGraphRunnerProjectsNodeInputByContract(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	registerContractProbeNodeType(
		registry,
		dsl.StateContract{
			Fields: []dsl.StateFieldRef{
				{Path: "topic", Mode: dsl.StateAccessRead},
				{Path: "result", Mode: dsl.StateAccessWrite, Required: true},
			},
		},
		nil,
		func(state State) string {
			if _, ok := state["secret"]; ok {
				return "leaked"
			}
			if state["topic"] == "weather" {
				return "clean"
			}
			return "missing"
		},
	)

	graph, err := registry.BuildGraph(GraphDefinition{
		EntryPoint:  "probe",
		FinishPoint: "probe",
		Nodes: []GraphNodeSpec{
			{ID: "probe", Type: "contract_probe"},
		},
	}, &BuildContext{})
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}

	runner := newContractTestRunner(t, graph)
	run, finalState, err := runner.Start(context.Background(), State{
		"topic":  "weather",
		"secret": "hidden",
	})
	if err != nil {
		t.Fatalf("start runner: %v", err)
	}
	if run.Status != fruntime.RunStatusCompleted {
		t.Fatalf("expected completed run, got %q", run.Status)
	}
	if got := finalState["result"]; got != "clean" {
		t.Fatalf("expected projected input to hide secret, got %#v", finalState)
	}
	if got := finalState["secret"]; got != "hidden" {
		t.Fatalf("expected merge to preserve full state, got %#v", finalState)
	}
}

func TestGraphRunnerRejectsUndeclaredPatchWrite(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	registerContractProbeNodeType(
		registry,
		dsl.StateContract{
			Fields: []dsl.StateFieldRef{
				{Path: "result", Mode: dsl.StateAccessWrite},
			},
		},
		func(state State) {
			state["secret"] = "mutated"
		},
		func(state State) string {
			return "ok"
		},
	)

	graph, err := registry.BuildGraph(GraphDefinition{
		EntryPoint:  "probe",
		FinishPoint: "probe",
		Nodes: []GraphNodeSpec{
			{ID: "probe", Type: "contract_probe"},
		},
	}, &BuildContext{})
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}

	runner := newContractTestRunner(t, graph)
	run, _, err := runner.Start(context.Background(), State{})
	if err == nil {
		t.Fatal("expected undeclared patch write to fail")
	}
	if !strings.Contains(err.Error(), `not declared as writable`) {
		t.Fatalf("expected undeclared write error, got %v", err)
	}
	if run.Status != fruntime.RunStatusFailed {
		t.Fatalf("expected failed run, got %q", run.Status)
	}
}

func TestGraphRunnerRejectsMissingRequiredRead(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	registerContractProbeNodeType(
		registry,
		dsl.StateContract{
			Fields: []dsl.StateFieldRef{
				{Path: "topic", Mode: dsl.StateAccessRead, Required: true},
				{Path: "result", Mode: dsl.StateAccessWrite},
			},
		},
		nil,
		func(state State) string {
			return "ok"
		},
	)

	graph, err := registry.BuildGraph(GraphDefinition{
		EntryPoint:  "probe",
		FinishPoint: "probe",
		Nodes: []GraphNodeSpec{
			{ID: "probe", Type: "contract_probe"},
		},
	}, &BuildContext{})
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}

	runner := newContractTestRunner(t, graph)
	run, _, err := runner.Start(context.Background(), State{})
	if err == nil {
		t.Fatal("expected missing required read to fail")
	}
	if !strings.Contains(err.Error(), `requires input path "shared.topic"`) {
		t.Fatalf("expected required read error, got %v", err)
	}
	if run.Status != fruntime.RunStatusFailed {
		t.Fatalf("expected failed run, got %q", run.Status)
	}
}

func TestGraphRunnerRejectsMissingRequiredWrite(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	registerContractProbeNodeType(
		registry,
		dsl.StateContract{
			Fields: []dsl.StateFieldRef{
				{Path: "result", Mode: dsl.StateAccessWrite, Required: true},
			},
		},
		nil,
		nil,
	)

	graph, err := registry.BuildGraph(GraphDefinition{
		EntryPoint:  "probe",
		FinishPoint: "probe",
		Nodes: []GraphNodeSpec{
			{ID: "probe", Type: "contract_probe"},
		},
	}, &BuildContext{})
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}

	runner := newContractTestRunner(t, graph)
	run, _, err := runner.Start(context.Background(), State{})
	if err == nil {
		t.Fatal("expected missing required write to fail")
	}
	if !strings.Contains(err.Error(), `must write path "shared.result"`) {
		t.Fatalf("expected required write error, got %v", err)
	}
	if run.Status != fruntime.RunStatusFailed {
		t.Fatalf("expected failed run, got %q", run.Status)
	}
}

func TestGraphRunnerIteratesWithRuntimePrivateStateContracts(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	registerCollectIteratorItemNodeType(registry)

	graph, err := registry.BuildGraph(GraphDefinition{
		EntryPoint:  "loop",
		FinishPoint: "loop",
		Nodes: []GraphNodeSpec{
			{
				ID:   "loop",
				Type: "iterator",
				Config: map[string]any{
					"state_key":      "payload.items",
					"max_iterations": 2,
					"continue_to":    "collect",
					"done_to":        dsl.EndNodeRef,
				},
			},
			{
				ID:   "collect",
				Type: "collect_iterator_item",
				Config: map[string]any{
					"iterator_node_id": "loop",
					"target_key":       "results",
				},
			},
		},
		Edges: []dsl.GraphEdgeSpec{
			{From: "collect", To: "loop"},
		},
	}, &BuildContext{})
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}

	runner := newContractTestRunner(t, graph)
	run, finalState, err := runner.Start(context.Background(), State{
		"payload": map[string]any{
			"items": []any{"alpha", "beta", "gamma"},
		},
	})
	if err != nil {
		t.Fatalf("start runner: %v", err)
	}
	if run.Status != fruntime.RunStatusCompleted {
		t.Fatalf("expected completed run, got %q", run.Status)
	}

	results, ok := finalState["results"].([]string)
	if !ok || len(results) != 2 || results[0] != "alpha" || results[1] != "beta" {
		t.Fatalf("expected collected iterator results, got %#v", finalState["results"])
	}

	runtimeState := finalState.Namespace(nodes.IteratorStateNamespace)
	if runtimeState == nil {
		t.Fatalf("expected runtime namespace in final state, got %#v", finalState)
	}
	loopState, ok := runtimeState["loop"].(map[string]any)
	if !ok {
		if typed, ok := runtimeState["loop"].(State); ok {
			loopState = typed
		} else {
			t.Fatalf("expected runtime loop state map, got %#v", runtimeState["loop"])
		}
	}
	if loopState["done"] != true {
		t.Fatalf("expected iterator to finish, got %#v", loopState)
	}
}
