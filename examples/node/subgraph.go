package main

import (
	"context"
	"fmt"

	"github.com/dengzii/weaveflow/node"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

func SubgraphExample() {
	subgraphNode := node.NewSubgraphNode()
	subgraphNode.GraphRef = "summarizer"
	subgraphNode.InputPath = state.Shared("subgraph_input")
	subgraphNode.OutputPath = state.Shared("subgraph_output")
	subgraphNode.RunChild = func(_ context.Context, request fruntime.ChildRunRequest, currentState *state.State) (fruntime.ChildRunResult, error) {
		input, _ := state.ReadPath(currentState, state.Shared("request", "input").String())
		fmt.Printf("  [subgraph %q] received input: %v\n", "summarizer", input)
		_ = state.SetPath(currentState, state.Shared("result").String(), map[string]any{
			"graph_ref": "summarizer", "summary": "The input was processed by the summarizer subgraph.",
		})
		return fruntime.ChildRunResult{
			Run: fruntime.RunRecord{
				RunID: request.ParentRunID + "/summarizer", ParentRunID: request.ParentRunID,
				ParentStepID: request.ParentStepID, ParentTaskID: request.ParentTaskID,
				Namespace: request.Namespace, Status: fruntime.RunStatusCompleted,
			},
			State: currentState,
		}, nil
	}

	currentState := state.FromShared(map[string]any{
		"subgraph_input": map[string]any{
			state.SectionShared: map[string]any{"request": map[string]any{"input": "Summarize the architecture of the WeaveFlow runtime."}},
		},
	})
	result, err := executeNode(context.Background(), subgraphNode, currentState)
	must(err)
	value, _ := readState(result, subgraphNode.OutputPath)
	printJSON(value)
}
