package weaveflow

import (
	"testing"

	fruntime "weaveflow/runtime"
)

func TestNewGraphRunnerCarriesResolvedNodeContracts(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	graph, err := registry.BuildGraph(GraphDefinition{
		EntryPoint:  "ask",
		FinishPoint: "ask",
		Nodes: []GraphNodeSpec{
			{
				ID:   "ask",
				Type: "human_message",
				Config: map[string]any{
					"state_scope": "agent",
				},
			},
		},
	}, &BuildContext{})
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}

	runner := NewGraphRunner(graph, nil, nil, fruntime.NewJSONStateCodec(""), nil)
	if runner.NodeContracts == nil {
		t.Fatal("expected runner node contracts to be initialized")
	}
	if _, ok := runner.NodeContracts["ask"]; !ok {
		t.Fatalf("expected human_message contract for node ask, got %#v", runner.NodeContracts)
	}
}
