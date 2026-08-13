package node

import (
	"fmt"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/state"
)

type ExecuteFunc func(ctx core.Context, access *state.Access) (core.NodeResult, error)

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

func (n *FuncNode) Execute(ctx core.Context, access *state.Access) (core.NodeResult, error) {
	if n == nil {
		return core.NodeResult{}, fmt.Errorf("node func node is nil")
	}
	if n.Fn == nil {
		return core.NodeResult{}, fmt.Errorf("node func node %q execute function is nil", n.ID())
	}
	return n.Fn(ctx, access)
}
