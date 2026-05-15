package state

import (
	"context"
)

type StatePatch State

type ExecutableNode interface {
	Execute(ctx context.Context, input State) (StatePatch, error)
}

func (p StatePatch) State() State {
	if p == nil {
		return nil
	}
	return State(p)
}
