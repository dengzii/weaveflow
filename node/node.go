// Package node provides reusable graph node implementations and construction options.
package node

import "github.com/dengzii/weaveflow/core"

type Node = core.Node
type Spec = core.NodeSpec
type Base = core.NodeBase
type Option = core.NodeOption

func WithID(id string) Option {
	return core.WithID(id)
}

func WithName(name string) Option {
	return core.WithName(name)
}

func NewBase(spec Spec) Base {
	return core.NewNodeBase(spec)
}

func applyNodeOptions(base *Base, options []Option) {
	core.ApplyNodeOptions(base, options)
}
