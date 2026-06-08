package main

import (
	"context"
	"fmt"
	"weaveflow/node"
	"weaveflow/state"
	"weaveflow/state/accessors"
)

func MappedSubgraphExample() {
	subgraphNode := node.NewMappedSubgraphNode()
	subgraphNode.GraphRef = "summarizer"
	subgraphNode.InputMappings = []node.PathMapping{
		{From: state.Shared(accessors.KeyRequest, accessors.RequestFieldInput), To: state.Shared(accessors.KeyRequest, accessors.RequestFieldInput)},
	}
	subgraphNode.OutputMappings = []node.PathMapping{
		{From: state.Shared("subgraph_result"), To: state.Shared("subgraph_result")},
	}
	subgraphNode.InvokeSubgraph = func(ctx context.Context, currentState *state.State) (*state.State, error) {
		input, _ := readState(currentState, state.Shared(accessors.KeyRequest, accessors.RequestFieldInput))
		fmt.Printf("  [mapped_subgraph %q] received input: %v\n", "summarizer", input)

		must(state.SetPath(currentState, state.Shared("subgraph_result").String(), map[string]any{
			"graph_ref": "summarizer",
			"summary":   "The input was processed by the summarizer mapped subgraph.",
		}))
		return currentState, nil
	}

	currentState := state.FromShared(map[string]any{
		accessors.KeyRequest: map[string]any{
			accessors.RequestFieldInput: "Summarize the architecture of the WeaveFlow runtime.",
		},
	})

	fmt.Println("input:")
	value, _ := readState(currentState, state.Shared(accessors.KeyRequest))
	fmt.Println(value)

	result, err := executeNode(context.Background(), subgraphNode, currentState)
	must(err)

	fmt.Println()
	fmt.Println("mapped subgraph result:")
	value, _ = readState(result, state.Shared("subgraph_result"))
	printJSON(value)
}
