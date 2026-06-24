package registry

import "github.com/dengzii/weaveflow/dsl"

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

func (r *Registry) StateFieldDefinitions() map[string]dsl.StateFieldDefinition {
	if r == nil || len(r.StateFields) == 0 {
		return map[string]dsl.StateFieldDefinition{}
	}
	out := make(map[string]dsl.StateFieldDefinition, len(r.StateFields))
	for key, def := range r.StateFields {
		out[key] = def
	}
	return out
}

func (r *Registry) NodeTypeDefinitions() map[string]NodeTypeDefinition {
	if r == nil || len(r.NodeTypes) == 0 {
		return map[string]NodeTypeDefinition{}
	}
	out := make(map[string]NodeTypeDefinition, len(r.NodeTypes))
	for key, def := range r.NodeTypes {
		out[key] = def
	}
	return out
}

func (r *Registry) ConditionDefinitions() map[string]ConditionDefinition {
	if r == nil || len(r.Conditions) == 0 {
		return map[string]ConditionDefinition{}
	}
	out := make(map[string]ConditionDefinition, len(r.Conditions))
	for key, def := range r.Conditions {
		out[key] = def
	}
	return out
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
