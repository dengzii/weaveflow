package registry

import (
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/node"
)

type NodeTypeDefinition struct {
	dsl.NodeTypeSchema
	Build                func(NodeBuildContext, dsl.GraphNodeSpec) (node.Node, error) `json:"-"`
	ResolveStateContract func(dsl.GraphNodeSpec) (dsl.StateContract, error)           `json:"-"`
}
