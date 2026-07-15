package node

import (
	"fmt"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

func primitivePort(name, description, schemaType string, mode dsl.StateAccessMode, required bool) dsl.StatePortDefinition {
	return dsl.StatePortDefinition{
		Name: name, Description: description, Required: required,
		Schema: dsl.JSONSchema{"type": schemaType}, Mode: mode, MergeStrategy: dsl.StateMergeReplace,
	}
}

func capabilityPort(name, description, capabilityID string, required bool, fields ...dsl.RelativeStateFieldRef) dsl.StatePortDefinition {
	return dsl.StatePortDefinition{
		Name: name, Description: description, Required: required, Capability: capabilityID,
		Contract: dsl.RelativeStateContract{Fields: append([]dsl.RelativeStateFieldRef(nil), fields...)},
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
