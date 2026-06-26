package node

import "github.com/dengzii/weaveflow/core"

type Node = core.Node
type AccessorUse = core.AccessorUse
type Spec = core.NodeSpec
type Base = core.NodeBase
type NodeOption = core.NodeOption

func Use(accessorName string) AccessorUse {
	return core.Use(accessorName)
}

func UseRoot(accessorName string) AccessorUse {
	return core.UseRoot(accessorName)
}

func UseScoped(accessorName string, scope string) AccessorUse {
	return core.UseScoped(accessorName, scope)
}

func WithID(id string) NodeOption {
	return core.WithID(id)
}

func WithName(name string) NodeOption {
	return core.WithName(name)
}

func WithScope(scope string) NodeOption {
	return core.WithScope(scope)
}

func NewBase(spec Spec) Base {
	return core.NewNodeBase(spec)
}

func applyNodeOptions(base *Base, options []NodeOption) {
	core.ApplyNodeOptions(base, options)
}
