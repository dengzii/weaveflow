package main

import (
	"context"
	"fmt"
	"weaveflow/nodes"
	wfstate "weaveflow/state"
)

func SubgraphExample() {
	node := nodes.NewSubgraphNode()
	node.GraphRef = "summarizer"
	node.InvokeSubgraph = func(ctx context.Context, state wfstate.State) (wfstate.State, error) {
		input, _ := state.ResolvePath("request.input")
		fmt.Printf("  [subgraph %q] received input: %v\n", "summarizer", input)

		state["subgraph_result"] = map[string]any{
			"graph_ref": "summarizer",
			"summary":   "The input was processed by the summarizer subgraph.",
		}
		return state, nil
	}

	state := wfstate.State{
		"request": map[string]any{
			"input": "Summarize the architecture of the WeaveFlow runtime.",
		},
	}

	fmt.Println("input:")
	fmt.Println(state["request"])

	result, err := node.Invoke(context.Background(), state)
	must(err)

	fmt.Println()
	fmt.Println("subgraph result:")
	printJSON(result["subgraph_result"])
}
