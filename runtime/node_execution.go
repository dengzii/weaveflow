package runtime

import (
	"context"
	"weaveflow/core"
)

type ExecutableNode = core.ExecutableNode[State]

type LegacyNodeExecutor struct {
	Invoke NodeInvoker
}

func (e LegacyNodeExecutor) Execute(ctx context.Context, input State) (State, error) {
	if e.Invoke == nil {
		return State{}, nil
	}
	next, err := e.Invoke(ctx, input.CloneState())
	if err != nil {
		return nil, err
	}
	return DiffState(input, next)
}
