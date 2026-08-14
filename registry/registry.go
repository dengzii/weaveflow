// Package registry stores validated node, condition, capability, and state module definitions.
package registry

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/state"
)

var reducerIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*\.v[1-9][0-9]*$`)

type Registry struct {
	stateModules      map[string]dsl.StateModuleDefinition
	capabilities      map[string]dsl.StateCapabilityDefinition
	nodeTypes         map[string]NodeTypeDefinition
	nodeGroups        map[string]NodeGroup
	conditions        map[string]ConditionDefinition
	stateFields       map[string]dsl.StateFieldDefinition
	capabilityModules map[string]string
	reducers          map[string]state.Reducer
}

func NewRegistry() *Registry {
	return &Registry{
		stateModules:      map[string]dsl.StateModuleDefinition{},
		capabilities:      map[string]dsl.StateCapabilityDefinition{},
		nodeTypes:         map[string]NodeTypeDefinition{},
		nodeGroups:        map[string]NodeGroup{},
		conditions:        map[string]ConditionDefinition{},
		stateFields:       map[string]dsl.StateFieldDefinition{},
		capabilityModules: map[string]string{},
		reducers:          map[string]state.Reducer{},
	}
}

func (r *Registry) RegisterReducer(identifier string, reducer state.Reducer) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	identifier = strings.TrimSpace(identifier)
	if !reducerIDPattern.MatchString(identifier) {
		return fmt.Errorf("reducer identifier %q must include a stable version", identifier)
	}
	if state.IsNilReducer(reducer) {
		return fmt.Errorf("reducer %q is nil", identifier)
	}
	if _, exists := r.reducers[identifier]; exists {
		return fmt.Errorf("reducer %q is already registered", identifier)
	}
	r.reducers[identifier] = reducer
	return nil
}

func (r *Registry) FindReducer(identifier string) (state.Reducer, bool) {
	if r == nil {
		return nil, false
	}
	reducer, ok := r.reducers[strings.TrimSpace(identifier)]
	if !ok || state.IsNilReducer(reducer) {
		return nil, false
	}
	return reducer, ok
}

func (r *Registry) ReducerIDs() []string {
	if r == nil || len(r.reducers) == 0 {
		return nil
	}
	identifiers := make([]string, 0, len(r.reducers))
	for identifier := range r.reducers {
		identifiers = append(identifiers, identifier)
	}
	sort.Strings(identifiers)
	return identifiers
}

func StateModuleKey(name, version string) string {
	return strings.TrimSpace(name) + "@" + strings.TrimSpace(version)
}

func (r *Registry) StateModuleDefinitions() map[string]dsl.StateModuleDefinition {
	if r == nil || len(r.stateModules) == 0 {
		return map[string]dsl.StateModuleDefinition{}
	}
	out := make(map[string]dsl.StateModuleDefinition, len(r.stateModules))
	for key, def := range r.stateModules {
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
	if r == nil || len(r.capabilities) == 0 {
		return map[string]dsl.StateCapabilityDefinition{}
	}
	out := make(map[string]dsl.StateCapabilityDefinition, len(r.capabilities))
	for key, def := range r.capabilities {
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
	if r == nil || len(r.nodeTypes) == 0 {
		return map[string]NodeTypeDefinition{}
	}
	out := make(map[string]NodeTypeDefinition, len(r.nodeTypes))
	for key, def := range r.nodeTypes {
		out[key] = cloneNodeTypeDefinition(def)
	}
	return out
}

func (r *Registry) NodeGroupDefinitions() map[string]NodeGroup {
	if r == nil || len(r.nodeGroups) == 0 {
		return map[string]NodeGroup{}
	}
	out := make(map[string]NodeGroup, len(r.nodeGroups))
	for key, group := range r.nodeGroups {
		out[key] = cloneNodeGroup(group)
	}
	return out
}

func (r *Registry) ConditionDefinitions() map[string]ConditionDefinition {
	if r == nil || len(r.conditions) == 0 {
		return map[string]ConditionDefinition{}
	}
	out := make(map[string]ConditionDefinition, len(r.conditions))
	for key, def := range r.conditions {
		out[key] = cloneConditionDefinition(def)
	}
	return out
}

func (r *Registry) JSONSchema() dsl.JSONSchema {
	nodeTypes := make(map[string]dsl.NodeTypeSchema, len(r.nodeTypes))
	for key, def := range r.nodeTypes {
		nodeTypes[key] = def.NodeTypeSchema
	}
	conditions := make(map[string]dsl.ConditionSchema, len(r.conditions))
	for key, def := range r.conditions {
		conditions[key] = def.ConditionSchema
	}
	return dsl.BuildGraphDefinitionSchema(r.stateModules, nodeTypes, conditions, r.ReducerIDs()...)
}

func (r *Registry) FindStateModule(name, version string) (dsl.StateModuleDefinition, bool) {
	if r == nil {
		return dsl.StateModuleDefinition{}, false
	}
	definition, ok := r.stateModules[StateModuleKey(name, version)]
	return cloneStateModuleDefinition(definition), ok
}

func (r *Registry) FindCapability(id string) (dsl.StateCapabilityDefinition, bool) {
	if r == nil {
		return dsl.StateCapabilityDefinition{}, false
	}
	definition, ok := r.capabilities[strings.TrimSpace(id)]
	return cloneCapabilityDefinition(definition), ok
}

func (r *Registry) FindNodeType(nodeType string) (NodeTypeDefinition, bool) {
	if r == nil {
		return NodeTypeDefinition{}, false
	}
	definition, ok := r.nodeTypes[strings.TrimSpace(nodeType)]
	return cloneNodeTypeDefinition(definition), ok
}

func (r *Registry) FindNodeGroup(name string) (NodeGroup, bool) {
	if r == nil {
		return NodeGroup{}, false
	}
	group, ok := r.nodeGroups[strings.TrimSpace(name)]
	return cloneNodeGroup(group), ok
}

func (r *Registry) FindCondition(conditionType string) (ConditionDefinition, bool) {
	if r == nil {
		return ConditionDefinition{}, false
	}
	definition, ok := r.conditions[strings.TrimSpace(conditionType)]
	return cloneConditionDefinition(definition), ok
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
