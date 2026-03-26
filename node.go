package falcon

import "context"

type NodeDef[S BaseState] interface {
	Name() string
	Description() string
	Invoke(ctx context.Context, state S) (S, error)
}

type StartNodeDef[S BaseState] struct {
}

func (s *StartNodeDef[S]) Name() string {
	return "start"
}

func (s *StartNodeDef[S]) Description() string {
	return "start node"
}

func (s *StartNodeDef[S]) Invoke(ctx context.Context, state S) (S, error) {
	return state, nil
}

type EndNodeDef[S BaseState] struct {
}

func (e *EndNodeDef[S]) Name() string {
	return "end"
}

func (e *EndNodeDef[S]) Description() string {
	return "end node"
}

func (e *EndNodeDef[S]) Invoke(ctx context.Context, state S) (S, error) {
	return state, nil
}
