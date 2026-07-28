package registry

import (
	"strings"

	"github.com/dengzii/weaveflow/dsl"
)

type Registry struct {
	StateModules map[string]dsl.StateModuleDefinition     `json:"state_modules"`
	Capabilities map[string]dsl.StateCapabilityDefinition `json:"capabilities"`
	NodeTypes    map[string]NodeTypeDefinition            `json:"node_types"`
	NodeGroups   map[string]NodeGroup                     `json:"node_groups"`
	Conditions   map[string]ConditionDefinition           `json:"conditions"`

	stateFields       map[string]dsl.StateFieldDefinition
	capabilityModules map[string]string
}

func NewRegistry() *Registry {
	return &Registry{
		StateModules:      map[string]dsl.StateModuleDefinition{},
		Capabilities:      map[string]dsl.StateCapabilityDefinition{},
		NodeTypes:         map[string]NodeTypeDefinition{},
		NodeGroups:        map[string]NodeGroup{},
		Conditions:        map[string]ConditionDefinition{},
		stateFields:       map[string]dsl.StateFieldDefinition{},
		capabilityModules: map[string]string{},
	}
}

func StateModuleKey(name, version string) string {
	return strings.TrimSpace(name) + "@" + strings.TrimSpace(version)
}

func (r *Registry) StateModuleDefinitions() map[string]dsl.StateModuleDefinition {
	if r == nil || len(r.StateModules) == 0 {
		return map[string]dsl.StateModuleDefinition{}
	}
	out := make(map[string]dsl.StateModuleDefinition, len(r.StateModules))
	for key, def := range r.StateModules {
		out[key] = cloneStateModuleDefinition(def)
	}
	return out
}

func (r *Registry) StateFieldDefinitions() map[string]dsl.StateFieldDefinition {
	if r == nil || len(r.stateFields) == 0 {
		return map[string]dsl.StateFieldDefinition{}
	}
	out := make(map[string]dsl.StateFieldDefinition, len(r.stateFields))
	for key, def := range r.stateFields {
		out[key] = cloneStateFieldDefinition(def)
	}
	return out
}

func (r *Registry) CapabilityDefinitions() map[string]dsl.StateCapabilityDefinition {
	if r == nil || len(r.Capabilities) == 0 {
		return map[string]dsl.StateCapabilityDefinition{}
	}
	out := make(map[string]dsl.StateCapabilityDefinition, len(r.Capabilities))
	for key, def := range r.Capabilities {
		out[key] = cloneCapabilityDefinition(def)
	}
	return out
}

func (r *Registry) CapabilityModule(id string) (string, bool) {
	if r == nil {
		return "", false
	}
	key, ok := r.capabilityModules[strings.TrimSpace(id)]
	return key, ok
}

func (r *Registry) NodeTypeDefinitions() map[string]NodeTypeDefinition {
	if r == nil || len(r.NodeTypes) == 0 {
		return map[string]NodeTypeDefinition{}
	}
	out := make(map[string]NodeTypeDefinition, len(r.NodeTypes))
	for key, def := range r.NodeTypes {
		out[key] = cloneNodeTypeDefinition(def)
	}
	return out
}

func (r *Registry) NodeGroupDefinitions() map[string]NodeGroup {
	if r == nil || len(r.NodeGroups) == 0 {
		return map[string]NodeGroup{}
	}
	out := make(map[string]NodeGroup, len(r.NodeGroups))
	for key, group := range r.NodeGroups {
		out[key] = cloneNodeGroup(group)
	}
	return out
}

func (r *Registry) ConditionDefinitions() map[string]ConditionDefinition {
	if r == nil || len(r.Conditions) == 0 {
		return map[string]ConditionDefinition{}
	}
	out := make(map[string]ConditionDefinition, len(r.Conditions))
	for key, def := range r.Conditions {
		out[key] = cloneConditionDefinition(def)
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
	return dsl.BuildGraphDefinitionSchema(r.StateModules, nodeTypes, conditions)
}

func cloneStateModuleDefinition(def dsl.StateModuleDefinition) dsl.StateModuleDefinition {
	cloned := def
	if len(def.Fields) > 0 {
		cloned.Fields = make([]dsl.StateFieldDefinition, len(def.Fields))
		for index, field := range def.Fields {
			cloned.Fields[index] = cloneStateFieldDefinition(field)
		}
	}
	if len(def.Capabilities) > 0 {
		cloned.Capabilities = make([]dsl.StateCapabilityDefinition, len(def.Capabilities))
		for index, capability := range def.Capabilities {
			cloned.Capabilities[index] = cloneCapabilityDefinition(capability)
		}
	}
	return cloned
}

func cloneCapabilityDefinition(def dsl.StateCapabilityDefinition) dsl.StateCapabilityDefinition {
	cloned := def
	cloned.Schema = def.Schema.Clone()
	if len(def.Fields) > 0 {
		cloned.Fields = make([]dsl.StateCapabilityFieldDefinition, len(def.Fields))
		for index, field := range def.Fields {
			cloned.Fields[index] = field
			cloned.Fields[index].Schema = field.Schema.Clone()
		}
	}
	return cloned
}

func cloneStateFieldDefinition(def dsl.StateFieldDefinition) dsl.StateFieldDefinition {
	cloned := def
	cloned.Schema = def.Schema.Clone()
	return cloned
}

func cloneNodeTypeDefinition(def NodeTypeDefinition) NodeTypeDefinition {
	cloned := def
	cloned.ConfigSchema = def.ConfigSchema.Clone()
	cloned.StatePorts = cloneStatePortDefinitions(def.StatePorts)
	cloned.NodeTypeSchema.StatePorts = cloneStatePortDefinitions(def.NodeTypeSchema.StatePorts)
	cloned.NodeTypeSchema.DynamicStatePorts = cloneDynamicStatePortDefinition(def.NodeTypeSchema.DynamicStatePorts)
	return cloned
}

func cloneNodeGroup(group NodeGroup) NodeGroup {
	cloned := group
	cloned.NodeTypes = append([]string(nil), group.NodeTypes...)
	return cloned
}

func cloneConditionDefinition(def ConditionDefinition) ConditionDefinition {
	cloned := def
	cloned.ConfigSchema = def.ConfigSchema.Clone()
	cloned.StatePorts = cloneStatePortDefinitions(def.StatePorts)
	cloned.ConditionSchema.StatePorts = cloneStatePortDefinitions(def.ConditionSchema.StatePorts)
	cloned.ConditionSchema.DynamicStatePorts = cloneDynamicStatePortDefinition(def.ConditionSchema.DynamicStatePorts)
	return cloned
}

func cloneDynamicStatePortDefinition(def *dsl.DynamicStatePortDefinition) *dsl.DynamicStatePortDefinition {
	if def == nil {
		return nil
	}
	cloned := *def
	cloned.Schema = def.Schema.Clone()
	return &cloned
}

func cloneStatePortDefinitions(ports []dsl.StatePortDefinition) []dsl.StatePortDefinition {
	if len(ports) == 0 {
		return nil
	}
	cloned := make([]dsl.StatePortDefinition, len(ports))
	for index, port := range ports {
		cloned[index] = port
		cloned[index].Schema = port.Schema.Clone()
		if len(port.Contract.Fields) > 0 {
			cloned[index].Contract.Fields = append([]dsl.RelativeStateFieldRef(nil), port.Contract.Fields...)
		}
	}
	return cloned
}
