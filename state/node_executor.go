package state

import (
	"context"
)

type NodeInvoker func(context.Context, State) (State, error)

type StatePatch = State

type ExecutableNode interface {
	Execute(ctx context.Context, input State) (StatePatch, error)
}

type LegacyNodeExecutor struct {
	Invoke NodeInvoker
}

func (e LegacyNodeExecutor) Execute(ctx context.Context, input State) (StatePatch, error) {
	if e.Invoke == nil {
		return StatePatch{}, nil
	}
	next, err := e.Invoke(ctx, input.CloneState())
	if err != nil {
		return nil, err
	}
	return DiffState(input, next)
}
