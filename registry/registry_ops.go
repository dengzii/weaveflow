package registry

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/state"
)

func (r *Registry) RegisterStateModule(def dsl.StateModuleDefinition) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	def.Name = strings.TrimSpace(def.Name)
	def.Version = strings.TrimSpace(def.Version)
	if def.Name == "" || def.Version == "" {
		return fmt.Errorf("state module name and version are required")
	}
	key := StateModuleKey(def.Name, def.Version)
	if _, exists := r.stateModules[key]; exists {
		return fmt.Errorf("state module %q version %q is already registered", def.Name, def.Version)
	}

	fieldPaths := map[string]struct{}{}
	for index := range def.Fields {
		field := &def.Fields[index]
		field.Path = strings.TrimSpace(field.Path)
		path, err := state.ParsePath(field.Path)
		if err != nil {
			return fmt.Errorf("state module %q field %q: %w", key, field.Path, err)
		}
		if path.Section() == state.SectionInternal || path.Section() == state.SectionRuntime {
			return fmt.Errorf("state module %q field %q uses reserved section %q", key, field.Path, path.Section())
		}
		if len(path.Segments()) == 0 {
			return fmt.Errorf("state module %q field %q must include a path below section %q", key, field.Path, path.Section())
		}
		if len(field.Schema) == 0 {
			return fmt.Errorf("state module %q field %q schema is required", key, field.Path)
		}
		if err := state.ValidateJSONSchemaDefinition(state.JSONSchema(field.Schema)); err != nil {
			return fmt.Errorf("state module %q field %q schema: %w", key, field.Path, err)
		}
		field.Path = path.String()
		if _, duplicate := fieldPaths[field.Path]; duplicate {
			return fmt.Errorf("state module %q field path %q is duplicated", key, field.Path)
		}
		if _, exists := r.stateFields[field.Path]; exists {
			return fmt.Errorf("state field path %q is already registered", field.Path)
		}
		fieldPaths[field.Path] = struct{}{}
	}

	capabilityIDs := map[string]struct{}{}
	for index := range def.Capabilities {
		capability := &def.Capabilities[index]
		capability.ID = strings.TrimSpace(capability.ID)
		if capability.ID == "" {
			return fmt.Errorf("state module %q capability id is required", key)
		}
		if _, duplicate := capabilityIDs[capability.ID]; duplicate {
			return fmt.Errorf("state module %q capability %q is duplicated", key, capability.ID)
		}
		if _, exists := r.capabilities[capability.ID]; exists {
			return fmt.Errorf("state capability %q is already registered", capability.ID)
		}
		capabilityIDs[capability.ID] = struct{}{}
		if err := validateCapabilityFields(*capability); err != nil {
			return fmt.Errorf("state module %q capability %q: %w", key, capability.ID, err)
		}
	}

	cloned := cloneStateModuleDefinition(def)
	r.stateModules[key] = cloned
	for _, field := range cloned.Fields {
		r.stateFields[field.Path] = field
	}
	for _, capability := range cloned.Capabilities {
		r.capabilities[capability.ID] = cloneCapabilityDefinition(capability)
		r.capabilityModules[capability.ID] = key
	}
	return nil
}

func validateCapabilityFields(def dsl.StateCapabilityDefinition) error {
	if err := state.ValidateJSONSchemaDefinition(state.JSONSchema(def.Schema)); err != nil {
		return fmt.Errorf("schema: %w", err)
	}
	if schemaType(def.Schema) != "object" {
		return fmt.Errorf("schema type must be object")
	}
	seen := map[string]struct{}{}
	for index := range def.Fields {
		field := &def.Fields[index]
		name, err := normalizeRelativePath(field.Name)
		if err != nil {
			return fmt.Errorf("field %q: %w", field.Name, err)
		}
		field.Name = name
		if _, exists := seen[field.Name]; exists {
			return fmt.Errorf("field %q is duplicated", field.Name)
		}
		seen[field.Name] = struct{}{}
		if len(field.Schema) == 0 {
			return fmt.Errorf("field %q schema is required", field.Name)
		}
		if err := state.ValidateJSONSchemaDefinition(state.JSONSchema(field.Schema)); err != nil {
			return fmt.Errorf("field %q schema: %w", field.Name, err)
		}
		if !validMergeStrategy(field.MergeStrategy) {
			return fmt.Errorf("field %q has invalid merge strategy %q", field.Name, field.MergeStrategy)
		}
	}
	return nil
}

func normalizeRelativePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("relative path is required")
	}
	parts := strings.Split(path, ".")
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
		if parts[index] == "" {
			return "", fmt.Errorf("relative path contains an empty segment")
		}
	}
	return strings.Join(parts, "."), nil
}

func schemaType(schema dsl.JSONSchema) string {
	value, _ := schema["type"].(string)
	return strings.TrimSpace(value)
}

func (r *Registry) RegisterNodeType(def NodeTypeDefinition) error {
	return r.registerNodeType("", def)
}

func (r *Registry) RegisterNodeTypeInGroup(group string, def NodeTypeDefinition) error {
	group = strings.TrimSpace(group)
	if group == "" {
		return fmt.Errorf("node group is required")
	}
	return r.registerNodeType(group, def)
}

func (r *Registry) registerNodeType(group string, def NodeTypeDefinition) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	def.Type = strings.TrimSpace(def.Type)
	if def.Type == "" {
		return fmt.Errorf("node type is required")
	}
	if def.Build == nil {
		return fmt.Errorf("node type %q builder is required", def.Type)
	}
	if len(def.StatePorts) == 0 {
		def.StatePorts = append([]dsl.StatePortDefinition(nil), def.NodeTypeSchema.StatePorts...)
	} else {
		def.NodeTypeSchema.StatePorts = append([]dsl.StatePortDefinition(nil), def.StatePorts...)
	}
	if err := validateStatePorts(def.StatePorts); err != nil {
		return fmt.Errorf("node type %q: %w", def.Type, err)
	}
	if err := validateDynamicStatePorts(def.DynamicStatePorts); err != nil {
		return fmt.Errorf("node type %q: %w", def.Type, err)
	}
	def.NodeTypeSchema.StatePorts = append([]dsl.StatePortDefinition(nil), def.StatePorts...)
	if _, exists := r.nodeTypes[def.Type]; exists {
		return fmt.Errorf("node type %q is already registered", def.Type)
	}
	r.nodeTypes[def.Type] = cloneNodeTypeDefinition(def)
	if group != "" {
		if r.nodeGroups == nil {
			r.nodeGroups = map[string]NodeGroup{}
		}
		nodeGroup := r.nodeGroups[group]
		nodeGroup.Name = group
		nodeGroup.NodeTypes = append(nodeGroup.NodeTypes, def.Type)
		r.nodeGroups[group] = nodeGroup
	}
	return nil
}

func (r *Registry) RegisterCondition(def ConditionDefinition) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	def.Type = strings.TrimSpace(def.Type)
	if def.Type == "" {
		return fmt.Errorf("condition type is required")
	}
	if def.Resolve == nil {
		return fmt.Errorf("condition %q resolver is required", def.Type)
	}
	if len(def.StatePorts) == 0 {
		def.StatePorts = append([]dsl.StatePortDefinition(nil), def.ConditionSchema.StatePorts...)
	} else {
		def.ConditionSchema.StatePorts = append([]dsl.StatePortDefinition(nil), def.StatePorts...)
	}
	if err := validateStatePorts(def.StatePorts); err != nil {
		return fmt.Errorf("condition %q: %w", def.Type, err)
	}
	if err := validateDynamicStatePorts(def.DynamicStatePorts); err != nil {
		return fmt.Errorf("condition %q: %w", def.Type, err)
	}
	def.ConditionSchema.StatePorts = append([]dsl.StatePortDefinition(nil), def.StatePorts...)
	if _, exists := r.conditions[def.Type]; exists {
		return fmt.Errorf("condition %q is already registered", def.Type)
	}
	r.conditions[def.Type] = cloneConditionDefinition(def)
	return nil
}

func validateStatePorts(ports []dsl.StatePortDefinition) error {
	seen := map[string]struct{}{}
	for index := range ports {
		port := &ports[index]
		port.Name = strings.TrimSpace(port.Name)
		port.Capability = strings.TrimSpace(port.Capability)
		port.DefaultPath = strings.TrimSpace(port.DefaultPath)
		if port.Name == "" {
			return fmt.Errorf("state port name is required")
		}
		if _, exists := seen[port.Name]; exists {
			return fmt.Errorf("state port %q is duplicated", port.Name)
		}
		seen[port.Name] = struct{}{}
		if port.DefaultPath != "" {
			defaultPath := strings.ReplaceAll(port.DefaultPath, "{node_id}", "node")
			parsed, err := state.ParsePath(defaultPath)
			if err != nil || len(parsed.Segments()) == 0 {
				if err == nil {
					err = fmt.Errorf("path must include a segment below its section")
				}
				return fmt.Errorf("state port %q default path %q: %w", port.Name, port.DefaultPath, err)
			}
		}
		primitive := port.Capability == ""
		if primitive {
			if len(port.Schema) == 0 {
				return fmt.Errorf("primitive state port %q requires schema", port.Name)
			}
			if err := state.ValidateJSONSchemaDefinition(state.JSONSchema(port.Schema)); err != nil {
				return fmt.Errorf("primitive state port %q schema: %w", port.Name, err)
			}
			if !validAccessMode(port.Mode) {
				return fmt.Errorf("primitive state port %q has invalid mode %q", port.Name, port.Mode)
			}
			if !validMergeStrategy(port.MergeStrategy) {
				return fmt.Errorf("primitive state port %q has invalid merge strategy %q", port.Name, port.MergeStrategy)
			}
			if len(port.Contract.Fields) > 0 {
				return fmt.Errorf("primitive state port %q cannot declare a relative contract", port.Name)
			}
			continue
		}
		if len(port.Schema) > 0 || port.Mode != "" || port.MergeStrategy != "" {
			return fmt.Errorf("capability state port %q cannot declare primitive schema, mode, or merge strategy", port.Name)
		}
		if len(port.Contract.Fields) == 0 {
			return fmt.Errorf("capability state port %q requires a relative contract", port.Name)
		}
		fields := map[string]struct{}{}
		for fieldIndex := range port.Contract.Fields {
			field := &port.Contract.Fields[fieldIndex]
			path, err := normalizeRelativePath(field.Path)
			if err != nil {
				return fmt.Errorf("capability state port %q field %q: %w", port.Name, field.Path, err)
			}
			field.Path = path
			if _, exists := fields[field.Path]; exists {
				return fmt.Errorf("capability state port %q field %q is duplicated", port.Name, field.Path)
			}
			fields[field.Path] = struct{}{}
			if !validAccessMode(field.Mode) {
				return fmt.Errorf("capability state port %q field %q has invalid mode %q", port.Name, field.Path, field.Mode)
			}
		}
	}
	return nil
}

func validateDynamicStatePorts(def *dsl.DynamicStatePortDefinition) error {
	if def == nil {
		return nil
	}
	def.NamePattern = strings.TrimSpace(def.NamePattern)
	if def.NamePattern == "" {
		return fmt.Errorf("dynamic state port name pattern is required")
	}
	if _, err := regexp.Compile("^(?:" + def.NamePattern + ")$"); err != nil {
		return fmt.Errorf("dynamic state port name pattern %q is invalid: %w", def.NamePattern, err)
	}
	if def.MinPorts < 0 {
		return fmt.Errorf("dynamic state port min_ports cannot be negative")
	}
	if def.MaxPorts < 0 {
		return fmt.Errorf("dynamic state port max_ports cannot be negative")
	}
	if def.MaxPorts > 0 && def.MaxPorts < def.MinPorts {
		return fmt.Errorf("dynamic state port max_ports cannot be less than min_ports")
	}
	if len(def.Schema) == 0 {
		return fmt.Errorf("dynamic state ports require schema")
	}
	if err := state.ValidateJSONSchemaDefinition(state.JSONSchema(def.Schema)); err != nil {
		return fmt.Errorf("dynamic state port schema: %w", err)
	}
	if def.Mode != dsl.StateAccessRead {
		return fmt.Errorf("dynamic state ports only support read mode")
	}
	if def.MergeStrategy != dsl.StateMergeReplace {
		return fmt.Errorf("dynamic state ports only support replace merge strategy")
	}
	return nil
}

func validAccessMode(mode dsl.StateAccessMode) bool {
	switch mode {
	case dsl.StateAccessRead, dsl.StateAccessWrite, dsl.StateAccessReadWrite:
		return true
	default:
		return false
	}
}

func validMergeStrategy(strategy dsl.StateMergeStrategy) bool {
	switch strategy {
	case dsl.StateMergeReplace, dsl.StateMergeMerge, dsl.StateMergeAppend:
		return true
	default:
		return false
	}
}

func (r *Registry) ResolveCondition(spec ResolvedConditionSpec) (EdgeCondition, error) {
	if r == nil {
		return EdgeCondition{}, fmt.Errorf("registry is nil")
	}
	spec.Spec = dsl.NormalizeGraphConditionSpec(spec.Spec)
	if spec.Spec.Type == "" {
		return EdgeCondition{}, fmt.Errorf("condition type is required")
	}
	conditionDef, ok := r.conditions[spec.Spec.Type]
	if !ok {
		return EdgeCondition{}, fmt.Errorf("condition %q is not registered", spec.Spec.Type)
	}
	condition, err := conditionDef.Resolve(spec)
	if err != nil {
		return EdgeCondition{}, err
	}
	return condition.WithSpec(spec.Spec), nil
}
