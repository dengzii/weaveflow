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

func (r *Registry) JSONSchema() dsl.JSONSchema {
	nodeTypes := make(map[string]dsl.NodeTypeSchema, len(r.NodeTypes))
	for key, def := range r.NodeTypes {
		nodeTypes[key] = def.NodeTypeSchema
	}
	conditions := make(map[string]dsl.ConditionSchema, len(r.Conditions))
	for key, def := range r.Conditions {
		conditions[key] = def.ConditionSchema
	}
	return dsl.BuildGraphDefinitionSchema(dsl.CommonStateSchemaID, r.StateFields, nodeTypes, conditions)
}
