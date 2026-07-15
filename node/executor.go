package node

import (
	"context"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/state"
)

type ExecutionResult = core.ExecutionResult

func Execute(ctx context.Context, base *state.State, target Node) (ExecutionResult, error) {
	ensureNodeID(target)
	return core.ExecuteNode(ctx, base, target)
}
