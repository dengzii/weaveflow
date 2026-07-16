package node

import (
	"fmt"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	executioncap "github.com/dengzii/weaveflow/capability/execution"
	plancap "github.com/dengzii/weaveflow/capability/plan"
	supervisorcap "github.com/dengzii/weaveflow/capability/supervisor"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

func primitivePort(name, description, schemaType string, mode dsl.StateAccessMode, required bool) dsl.StatePortDefinition {
	return dsl.StatePortDefinition{
		Name: name, Description: description, DefaultPath: defaultPrimitivePath(name), Required: required,
		Schema: dsl.JSONSchema{"type": schemaType}, Mode: mode, MergeStrategy: dsl.StateMergeReplace,
	}
}

func primitivePortWithDefault(name, description, schemaType string, mode dsl.StateAccessMode, required bool, defaultPath string) dsl.StatePortDefinition {
	port := primitivePort(name, description, schemaType, mode, required)
	port.DefaultPath = defaultPath
	return port
}

func capabilityPort(name, description, capabilityID string, required bool, fields ...dsl.RelativeStateFieldRef) dsl.StatePortDefinition {
	return dsl.StatePortDefinition{
		Name: name, Description: description, DefaultPath: defaultCapabilityPath(name, capabilityID), Required: required, Capability: capabilityID,
		Contract: dsl.RelativeStateContract{Fields: append([]dsl.RelativeStateFieldRef(nil), fields...)},
	}
}

func defaultPrimitivePath(name string) string {
	switch name {
	case "input", "task", "objective":
		return "shared.request.input"
	case "pending_input":
		return "shared.request.pending_input"
	case "output", "result":
		return "shared.final.answer"
	case "environment":
		return "shared.environment"
	default:
		return "shared." + name
	}
}

func defaultCapabilityPath(name, capabilityID string) string {
	switch capabilityID {
	case plancap.CapabilityID:
		return "shared.plan"
	case executioncap.CapabilityID:
		return "shared.execution"
	case supervisorcap.CapabilityID:
		return "shared.supervisor"
	case conversationcap.CapabilityID:
		return "scopes.{node_id}." + name
	default:
		return "scopes.{node_id}." + name
	}
}

func capabilityField(path string, mode dsl.StateAccessMode) dsl.RelativeStateFieldRef {
	return dsl.RelativeStateFieldRef{Path: path, Mode: mode}
}

func resolvedPath(spec registry.ResolvedNodeSpec, name string) (state.Path, error) {
	binding, ok := spec.State[name]
	if !ok || binding.Path.Empty() {
		return state.Path{}, fmt.Errorf("node %q requires resolved state port %q", spec.Spec.ID, name)
	}
	return binding.Path, nil
}

func optionalResolvedPath(spec registry.ResolvedNodeSpec, name string) state.Path {
	binding, ok := spec.State[name]
	if !ok {
		return state.Path{}
	}
	return binding.Path
}
