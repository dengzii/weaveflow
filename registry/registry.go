package registry

import "weaveflow/dsl"

type Registry struct {
	StateFields map[string]dsl.StateFieldDefinition `json:"state_fields"`
	NodeTypes   map[string]NodeTypeDefinition       `json:"node_types"`
	Conditions  map[string]ConditionDefinition      `json:"conditions"`
}

func NewRegistry() *Registry {
	return &Registry{
		StateFields: map[string]dsl.StateFieldDefinition{},
		NodeTypes:   map[string]NodeTypeDefinition{},
		Conditions:  map[string]ConditionDefinition{},
	}
}
