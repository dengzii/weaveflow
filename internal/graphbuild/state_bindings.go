package graphbuild

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

type ResolvedGraphBindings struct {
	Nodes                      map[string]registry.ResolvedNodeSpec
	Conditions                 map[int]registry.ResolvedConditionSpec
	NodeContracts              map[string]state.Contract
	ConditionContracts         map[int]state.Contract
	ConditionContractsBySource map[string]state.Contract
	StateSchemas               map[string]state.JSONSchema
	InitialStatePaths          []string
}

type bindingResolver struct {
	registry         *registry.Registry
	modules          map[string]dsl.StateModuleDefinition
	fields           map[string]dsl.StateFieldDefinition
	capabilities     map[string]dsl.StateCapabilityDefinition
	rootCapabilities map[string]string
}

func ResolveGraphBindings(def dsl.GraphDefinition, reg *registry.Registry) (ResolvedGraphBindings, error) {
	if reg == nil {
		return ResolvedGraphBindings{}, fmt.Errorf("registry is nil")
	}
	resolver, err := newBindingResolver(def, reg)
	if err != nil {
		return ResolvedGraphBindings{}, err
	}
	result := ResolvedGraphBindings{
		Nodes:                      make(map[string]registry.ResolvedNodeSpec, len(def.Nodes)),
		Conditions:                 map[int]registry.ResolvedConditionSpec{},
		NodeContracts:              make(map[string]state.Contract, len(def.Nodes)),
		ConditionContracts:         map[int]state.Contract{},
		ConditionContractsBySource: map[string]state.Contract{},
		StateSchemas:               cloneStateSchemas(resolver.fields),
		InitialStatePaths:          sortedFieldPaths(resolver.fields),
	}
	for _, spec := range def.Nodes {
		definition, ok := reg.FindNodeType(spec.Type)
		if !ok {
			return ResolvedGraphBindings{}, fmt.Errorf("node type %q is not registered", spec.Type)
		}
		bindings, contract, resolveErr := resolver.resolvePorts("node "+fmt.Sprintf("%q", spec.ID), spec.ID, definition.StatePorts, definition.DynamicStatePorts, spec.State)
		if resolveErr != nil {
			return ResolvedGraphBindings{}, resolveErr
		}
		spec.State = stateBindingSpecs(bindings)
		result.Nodes[spec.ID] = registry.ResolvedNodeSpec{Spec: spec, State: bindings}
		result.NodeContracts[spec.ID] = contract
	}
	for index, edge := range def.Edges {
		if edge.Condition == nil {
			continue
		}
		definition, ok := reg.FindCondition(edge.Condition.Type)
		if !ok {
			return ResolvedGraphBindings{}, fmt.Errorf("condition %q is not registered", edge.Condition.Type)
		}
		label := fmt.Sprintf("condition %q on edge %q -> %q", edge.Condition.Type, edge.From, edge.To)
		bindings, contract, resolveErr := resolver.resolvePorts(label, edge.From, definition.StatePorts, definition.DynamicStatePorts, edge.Condition.State)
		if resolveErr != nil {
			return ResolvedGraphBindings{}, resolveErr
		}
		conditionSpec := *edge.Condition
		conditionSpec.State = stateBindingSpecs(bindings)
		result.Conditions[index] = registry.ResolvedConditionSpec{Spec: conditionSpec, State: bindings}
		result.ConditionContracts[index] = contract
		combined, combineErr := mergeContracts(result.ConditionContractsBySource[edge.From], contract)
		if combineErr != nil {
			return ResolvedGraphBindings{}, fmt.Errorf("conditions from node %q: %w", edge.From, combineErr)
		}
		result.ConditionContractsBySource[edge.From] = combined
	}
	return result, nil
}

func StateBindingSemantics(resolved ResolvedGraphBindings) []dsl.StateBindingSemantic {
	bindings := make([]dsl.StateBindingSemantic, 0)
	nodeIDs := make([]string, 0, len(resolved.Nodes))
	for nodeID := range resolved.Nodes {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Strings(nodeIDs)
	for _, nodeID := range nodeIDs {
		bindings = appendResolvedBindingSemantics(bindings, "node", nodeID, 0, resolved.Nodes[nodeID].State)
	}

	edgeIndexes := make([]int, 0, len(resolved.Conditions))
	for edgeIndex := range resolved.Conditions {
		edgeIndexes = append(edgeIndexes, edgeIndex)
	}
	sort.Ints(edgeIndexes)
	for _, edgeIndex := range edgeIndexes {
		bindings = appendResolvedBindingSemantics(bindings, "condition", "", edgeIndex, resolved.Conditions[edgeIndex].State)
	}
	return bindings
}

func appendResolvedBindingSemantics(target []dsl.StateBindingSemantic, componentType, componentID string, edgeIndex int, bindings map[string]registry.ResolvedStateBinding) []dsl.StateBindingSemantic {
	ports := make([]string, 0, len(bindings))
	for port := range bindings {
		ports = append(ports, port)
	}
	sort.Strings(ports)
	for _, port := range ports {
		binding := bindings[port]
		semantic := dsl.StateBindingSemantic{
			ComponentType: componentType,
			ComponentID:   componentID,
			EdgeIndex:     edgeIndex,
			Port:          port,
			Path:          binding.Path.String(),
			Capability:    binding.Capability,
		}
		for _, field := range binding.Contract.Fields {
			semantic.Contract = append(semantic.Contract, dsl.StateContractSemanticField{
				Path: field.Path.String(), Mode: dsl.StateAccessMode(field.Mode), Required: field.Required, MergeStrategy: dsl.StateMergeStrategy(field.Merge), Type: field.Type,
			})
		}
		target = append(target, semantic)
	}
	return target
}

func newBindingResolver(def dsl.GraphDefinition, reg *registry.Registry) (*bindingResolver, error) {
	resolver := &bindingResolver{
		registry:         reg,
		modules:          map[string]dsl.StateModuleDefinition{},
		fields:           map[string]dsl.StateFieldDefinition{},
		capabilities:     map[string]dsl.StateCapabilityDefinition{},
		rootCapabilities: map[string]string{},
	}
	for _, ref := range def.StateModules {
		key := registry.StateModuleKey(ref.Name, ref.Version)
		module, ok := reg.FindStateModule(ref.Name, ref.Version)
		if !ok {
			return nil, fmt.Errorf("state module %q version %q is not registered", ref.Name, ref.Version)
		}
		resolver.modules[key] = module
		for _, field := range module.Fields {
			resolver.fields[field.Path] = field
		}
		for _, capability := range module.Capabilities {
			resolver.capabilities[capability.ID] = capability
		}
	}
	return resolver, nil
}

func (r *bindingResolver) resolvePorts(component, ownerID string, ports []dsl.StatePortDefinition, dynamic *dsl.DynamicStatePortDefinition, specs map[string]dsl.StateBinding) (map[string]registry.ResolvedStateBinding, state.Contract, error) {
	portDefinitions := make(map[string]dsl.StatePortDefinition, len(ports)+len(specs))
	for _, port := range ports {
		portDefinitions[port.Name] = port
	}
	dynamicNames := make([]string, 0)
	if dynamic != nil {
		pattern, err := regexp.Compile("^(?:" + dynamic.NamePattern + ")$")
		if err != nil {
			return nil, state.Contract{}, fmt.Errorf("%s has invalid dynamic state port pattern: %w", component, err)
		}
		for name := range specs {
			if _, static := portDefinitions[name]; static {
				continue
			}
			if !pattern.MatchString(name) {
				return nil, state.Contract{}, fmt.Errorf("%s binds unknown state port %q", component, name)
			}
			dynamicNames = append(dynamicNames, name)
		}
		if len(dynamicNames) < dynamic.MinPorts {
			return nil, state.Contract{}, fmt.Errorf("%s requires at least %d dynamic state ports", component, dynamic.MinPorts)
		}
		if dynamic.MaxPorts > 0 && len(dynamicNames) > dynamic.MaxPorts {
			return nil, state.Contract{}, fmt.Errorf("%s allows at most %d dynamic state ports", component, dynamic.MaxPorts)
		}
		sort.Strings(dynamicNames)
		for _, name := range dynamicNames {
			portDefinitions[name] = dsl.StatePortDefinition{
				Name:          name,
				Description:   dynamic.Description,
				Required:      dynamic.Required,
				Schema:        dynamic.Schema,
				Mode:          dynamic.Mode,
				MergeStrategy: dynamic.MergeStrategy,
			}
		}
	} else {
		for name := range specs {
			if _, ok := portDefinitions[name]; !ok {
				return nil, state.Contract{}, fmt.Errorf("%s binds unknown state port %q", component, name)
			}
		}
	}

	bindings := make(map[string]registry.ResolvedStateBinding, len(specs))
	contract := state.Contract{}
	resolvedPorts := make([]dsl.StatePortDefinition, 0, len(ports)+len(dynamicNames))
	resolvedPorts = append(resolvedPorts, ports...)
	for _, name := range dynamicNames {
		resolvedPorts = append(resolvedPorts, portDefinitions[name])
	}
	for _, port := range resolvedPorts {
		binding, exists := specs[port.Name]
		pathText := ""
		if exists {
			pathText = strings.TrimSpace(binding.Path)
		}
		if pathText == "" {
			pathText = expandDefaultPath(port.DefaultPath, ownerID)
		}
		if pathText == "" {
			if port.Required {
				return nil, state.Contract{}, fmt.Errorf("%s requires state port %q", component, port.Name)
			}
			continue
		}
		path, err := state.ParsePath(pathText)
		if err != nil {
			return nil, state.Contract{}, fmt.Errorf("%s state port %q path %q: %w", component, port.Name, pathText, err)
		}
		if len(path.Segments()) == 0 {
			return nil, state.Contract{}, fmt.Errorf("%s state port %q cannot bind a state section root", component, port.Name)
		}
		if path.Section() == state.SectionInternal || path.Section() == state.SectionRuntime {
			return nil, state.Contract{}, fmt.Errorf("%s state port %q cannot bind reserved path %q", component, port.Name, path.String())
		}

		resolved := registry.ResolvedStateBinding{Path: path, Capability: strings.TrimSpace(port.Capability)}
		if resolved.Capability == "" {
			fieldSchema := port.Schema.Clone()
			if moduleField, ok := r.fields[path.String()]; ok && len(moduleField.Schema) > 0 {
				fieldSchema = moduleField.Schema.Clone()
			}
			resolved.Contract = state.NewContract(state.FieldAccess{
				Path:        path,
				Mode:        state.AccessMode(port.Mode),
				Required:    port.Required && canRead(port.Mode),
				Merge:       effectiveMerge(state.MergeStrategy(port.MergeStrategy)),
				Type:        schemaType(port.Schema),
				Schema:      state.JSONSchema(fieldSchema),
				Description: port.Description,
			})
			if field, ok := r.fields[path.String()]; ok && !schemasCompatible(field.Schema, port.Schema) {
				return nil, state.Contract{}, fmt.Errorf("%s state port %q schema type %q conflicts with module field %q type %q", component, port.Name, schemaType(port.Schema), path.String(), schemaType(field.Schema))
			}
		} else {
			capability, ok := r.capabilities[resolved.Capability]
			if !ok {
				if _, registered := r.registry.FindCapability(resolved.Capability); registered {
					return nil, state.Contract{}, fmt.Errorf("%s state port %q capability %q belongs to an unreferenced state module", component, port.Name, resolved.Capability)
				}
				return nil, state.Contract{}, fmt.Errorf("%s state port %q capability %q is not registered", component, port.Name, resolved.Capability)
			}
			if existing, ok := r.rootCapabilities[path.String()]; ok && existing != resolved.Capability {
				return nil, state.Contract{}, fmt.Errorf("state path %q is bound to incompatible capabilities %q and %q", path.String(), existing, resolved.Capability)
			}
			r.rootCapabilities[path.String()] = resolved.Capability
			if field, ok := r.fields[path.String()]; ok && !schemasCompatible(field.Schema, capability.Schema) {
				return nil, state.Contract{}, fmt.Errorf("%s state port %q capability %q conflicts with module field %q schema", component, port.Name, resolved.Capability, path.String())
			}
			resolved.Contract, err = expandCapabilityContract(path, capability, port.Contract, port.Description)
			if err != nil {
				return nil, state.Contract{}, fmt.Errorf("%s state port %q: %w", component, port.Name, err)
			}
			for _, expanded := range resolved.Contract.Fields {
				field, ok := r.fields[expanded.Path.String()]
				if !ok {
					continue
				}
				moduleType := schemaType(field.Schema)
				if expanded.Type != "" && moduleType != "" && expanded.Type != moduleType {
					return nil, state.Contract{}, fmt.Errorf("%s state port %q capability field %q type %q conflicts with module field %q type %q", component, port.Name, expanded.Path.String(), expanded.Type, field.Path, moduleType)
				}
			}
		}
		merged, mergeErr := mergeContracts(contract, resolved.Contract)
		if mergeErr != nil {
			return nil, state.Contract{}, fmt.Errorf("%s state port %q: %w", component, port.Name, mergeErr)
		}
		contract = merged
		bindings[port.Name] = resolved
	}
	return bindings, contract, nil
}

func expandDefaultPath(template, ownerID string) string {
	template = strings.TrimSpace(template)
	if template == "" {
		return ""
	}
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		ownerID = "node"
	}
	ownerID = strings.ReplaceAll(ownerID, ".", "_")
	return strings.ReplaceAll(template, "{node_id}", ownerID)
}

func stateBindingSpecs(bindings map[string]registry.ResolvedStateBinding) map[string]dsl.StateBinding {
	if len(bindings) == 0 {
		return nil
	}
	specs := make(map[string]dsl.StateBinding, len(bindings))
	for name, binding := range bindings {
		specs[name] = dsl.StateBinding{Path: binding.Path.String()}
	}
	return specs
}

func expandCapabilityContract(root state.Path, capability dsl.StateCapabilityDefinition, relative dsl.RelativeStateContract, description string) (state.Contract, error) {
	fields := make(map[string]dsl.StateCapabilityFieldDefinition, len(capability.Fields))
	for _, field := range capability.Fields {
		fields[field.Name] = field
	}
	contract := state.Contract{}
	seen := map[string]struct{}{}
	for _, reference := range relative.Fields {
		name := strings.TrimSpace(reference.Path)
		if _, duplicate := seen[name]; duplicate {
			return state.Contract{}, fmt.Errorf("relative field %q is duplicated", name)
		}
		seen[name] = struct{}{}
		field, ok := fields[name]
		if !ok {
			return state.Contract{}, fmt.Errorf("capability %q has no field %q", capability.ID, name)
		}
		path, err := root.Child(strings.Split(name, ".")...)
		if err != nil {
			return state.Contract{}, fmt.Errorf("expand field %q: %w", name, err)
		}
		contract.Fields = append(contract.Fields, state.FieldAccess{
			Path:        path,
			Mode:        state.AccessMode(reference.Mode),
			Required:    reference.Required && canRead(reference.Mode),
			Merge:       state.MergeStrategy(field.MergeStrategy),
			Type:        schemaType(field.Schema),
			Schema:      state.JSONSchema(field.Schema.Clone()),
			Description: description,
		})
	}
	return contract, nil
}

func mergeContracts(left, right state.Contract) (state.Contract, error) {
	result := left.Clone()
	indexes := make(map[string]int, len(result.Fields))
	for index, field := range result.Fields {
		indexes[field.Path.String()] = index
	}
	for _, field := range right.Fields {
		key := field.Path.String()
		index, exists := indexes[key]
		if !exists {
			indexes[key] = len(result.Fields)
			result.Fields = append(result.Fields, field)
			continue
		}
		existing := result.Fields[index]
		if existing.Type != "" && field.Type != "" && existing.Type != field.Type {
			return state.Contract{}, fmt.Errorf("state path %q has incompatible schema types %q and %q", key, existing.Type, field.Type)
		}
		if effectiveMerge(existing.Merge) != effectiveMerge(field.Merge) {
			return state.Contract{}, fmt.Errorf("state path %q has incompatible merge strategies %q and %q", key, existing.Merge, field.Merge)
		}
		existing.Mode = mergeAccessModes(existing.Mode, field.Mode)
		existing.Required = existing.Required || field.Required
		if existing.Type == "" {
			existing.Type = field.Type
		}
		if len(existing.Schema) == 0 {
			existing.Schema = field.Schema.Clone()
		} else if len(field.Schema) > 0 && !schemasCompatible(dsl.JSONSchema(existing.Schema), dsl.JSONSchema(field.Schema)) {
			return state.Contract{}, fmt.Errorf("state path %q has incompatible schemas", key)
		}
		existing.Merge = effectiveMerge(existing.Merge)
		result.Fields[index] = existing
	}
	return result, nil
}

func mergeAccessModes(left, right state.AccessMode) state.AccessMode {
	if left == right {
		return left
	}
	if left == state.AccessReadWrite || right == state.AccessReadWrite {
		return state.AccessReadWrite
	}
	return state.AccessReadWrite
}

func canRead(mode dsl.StateAccessMode) bool {
	return mode == dsl.StateAccessRead || mode == dsl.StateAccessReadWrite
}

func effectiveMerge(strategy state.MergeStrategy) state.MergeStrategy {
	if strategy == "" {
		return state.MergeReplace
	}
	return strategy
}

func schemasCompatible(left, right dsl.JSONSchema) bool {
	leftType := schemaType(left)
	rightType := schemaType(right)
	return leftType == "" || rightType == "" || leftType == rightType
}

func schemaType(schema dsl.JSONSchema) string {
	value, _ := schema["type"].(string)
	return strings.TrimSpace(value)
}

func sortedFieldPaths(fields map[string]dsl.StateFieldDefinition) []string {
	paths := make([]string, 0, len(fields))
	for path := range fields {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func cloneStateSchemas(fields map[string]dsl.StateFieldDefinition) map[string]state.JSONSchema {
	if len(fields) == 0 {
		return nil
	}
	schemas := make(map[string]state.JSONSchema, len(fields))
	for path, field := range fields {
		schemas[path] = state.JSONSchema(field.Schema.Clone())
	}
	return schemas
}
