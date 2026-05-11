package registry

import (
	"weaveflow/core"
	"weaveflow/dsl"
	fruntime "weaveflow/runtime"
)

type NodeTypeDefinition struct {
	dsl.NodeTypeSchema
	Build                func(NodeBuildContext, dsl.GraphNodeSpec) (core.Node[fruntime.State], error) `json:"-"`
	ResolveStateContract func(dsl.GraphNodeSpec) (dsl.StateContract, error)                           `json:"-"`
}
