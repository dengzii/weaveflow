package node

import (
	"context"
	"fmt"

	"weaveflow/core"
	"weaveflow/state"
)

type ExecutionResult struct {
	State    *state.State
	Patch    state.Patch
	Contract state.Contract
}

func Execute(ctx context.Context, registry *state.Registry, base *state.State, node Node) (ExecutionResult, error) {
	if node == nil {
		return ExecutionResult{}, fmt.Errorf("node node is nil")
	}
	if err := ctx.Err(); err != nil {
		return ExecutionResult{}, err
	}
	EnsureNodeID(node)
	if specNode, ok := node.(interface{ Validate() error }); ok {
		if err := specNode.Validate(); err != nil {
			return ExecutionResult{}, err
		}
	}

	contract, err := ContractFor(registry, node)
	if err != nil {
		return ExecutionResult{}, err
	}

	access := state.NewEditingAccess(registry, base).WithScope(node.Scope())
	if err := node.Execute(core.NewContext(ctx), access); err != nil {
		return ExecutionResult{}, err
	}
	return ExecutionResult{
		State:    access.State(),
		Patch:    access.Patch(),
		Contract: contract,
	}, nil
}
