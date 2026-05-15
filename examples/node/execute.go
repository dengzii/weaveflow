package main

import (
	"context"
	"weaveflow/core"
	wfstate "weaveflow/state"
)

type executableNode interface {
	Execute(context.Context, wfstate.State) (wfstate.StatePatch, error)
}

func executeNode(ctx context.Context, node executableNode, state wfstate.State) (wfstate.State, error) {
	patch, err := node.Execute(ctx, state)
	if err != nil {
		return state, err
	}
	merged, _, err := wfstate.MergeStatePatch(state, patch, wfstate.StatePatchMergeOptions{
		Contract: core.NodeIOContract{WildcardWrite: true},
	})
	if err != nil {
		return state, err
	}
	return merged, nil
}
