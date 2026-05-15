package graph

import (
	"testing"
	"weaveflow/builder"
	"weaveflow/builtin"
	"weaveflow/dsl"

	wfstate "weaveflow/state"
)

func TestNewGraphRunnerCarriesResolvedNodeContracts(t *testing.T) {
	t.Parallel()

	registry := builtin.NewDefaultRegistry()
	graph, err := BuildGraph(registry, dsl.GraphDefinition{
		EntryPoint:  "ask",
		FinishPoint: "ask",
		Nodes: []dsl.GraphNodeSpec{
			{
				ID:   "ask",
				Type: "human_message",
				Config: map[string]any{
					"state_scope": "agent",
				},
			},
		},
	}, &builder.BuildContext{})
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}

	runner := NewGraphRunner(graph, nil, nil, wfstate.NewJSONStateCodec(""), nil)
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
