package nodes

import (
	"context"
	"testing"
	"weaveflow/core"
	wfstate "weaveflow/state"
)

func runTestNode(t *testing.T, node core.Node[wfstate.State, wfstate.StatePatch], ctx context.Context, state wfstate.State) (wfstate.State, error) {
	t.Helper()
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
