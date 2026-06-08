package node

import (
	"context"
	"fmt"

	"weaveflow/state"
)

type ExecuteFunc func(ctx context.Context, access *state.Access) error

type FuncNode struct {
	Base
	Fn ExecuteFunc
}

func NewFuncNode(spec Spec, fn ExecuteFunc) *FuncNode {
	return &FuncNode{
		Base: NewBase(spec),
		Fn:   fn,
	}
}

func (n *FuncNode) Execute(ctx context.Context, access *state.Access) error {
	if n == nil {
		return fmt.Errorf("node func node is nil")
	}
	if n.Fn == nil {
		return fmt.Errorf("node func node %q execute function is nil", n.ID())
	}
	return n.Fn(ctx, access)
}
