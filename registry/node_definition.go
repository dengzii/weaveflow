package registry

import (
	"weaveflow/core"
	"weaveflow/dsl"
	wfstate "weaveflow/state"
)

type NodeTypeDefinition struct {
	dsl.NodeTypeSchema
	Build                func(NodeBuildContext, dsl.GraphNodeSpec) (core.Node[wfstate.State, wfstate.StatePatch], error) `json:"-"`
	ResolveStateContract func(dsl.GraphNodeSpec) (dsl.StateContract, error)                                              `json:"-"`
}
