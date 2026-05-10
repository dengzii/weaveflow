package weaveflow

import (
	"testing"
	"weaveflow/dsl"

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

func TestConvertStateContractSplitsReadWriteWildcard(t *testing.T) {
	t.Parallel()

	contract := convertStateContract(dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{Path: "*", Mode: dsl.StateAccessRead},
			{Path: "result", Mode: dsl.StateAccessWrite},
		},
	})

	if !contract.WildcardRead {
		t.Fatal("expected wildcard read to be enabled")
	}
	if contract.WildcardWrite {
		t.Fatal("expected wildcard write to stay disabled")
	}
	if len(contract.WritePaths) != 1 || contract.WritePaths[0] != "shared.result" {
		t.Fatalf("expected explicit write path to be preserved, got %#v", contract)
	}
}
