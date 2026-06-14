package registry

import (
	"github.com/dengzii/weaveflow/dsl"
)

type NodeTypeDefinition struct {
	dsl.NodeTypeSchema
	Build                NodeBuilder                                        `json:"-"`
	ResolveStateContract func(dsl.GraphNodeSpec) (dsl.StateContract, error) `json:"-"`
}
