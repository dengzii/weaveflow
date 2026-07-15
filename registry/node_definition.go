package registry

import (
	"github.com/dengzii/weaveflow/dsl"
)

type NodeTypeDefinition struct {
	dsl.NodeTypeSchema
	StatePorts []dsl.StatePortDefinition `json:"state_ports"`
	Build      NodeBuilder               `json:"-"`
}
