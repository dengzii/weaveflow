package stateops

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

const (
	NodeTypeStateSet       = "state_set"
	NodeTypeStateCopy      = "state_copy"
	NodeTypeStateDelete    = "state_delete"
	NodeTypeStateMerge     = "state_merge"
	NodeTypeStateAppend    = "state_append"
	NodeTypeStateTransform = "state_transform"
)

var (
	_ dsl.GraphNodeSpecProvider = (*StateSetNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*StateCopyNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*StateDeleteNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*StateMergeNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*StateAppendNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*StateTransformNode)(nil)
)

func newBase(nodeType, description string, options ...core.NodeOption) core.NodeBase {
	base := core.NewNodeBase(core.NodeSpec{Name: nodeType, Description: description})
	core.ApplyNodeOptions(&base, options)
	return base
}

func validateBase(base *core.NodeBase, nodeType string) error {
	if base == nil {
		return fmt.Errorf("%s node is nil", nodeType)
	}
	if err := base.Validate(); err != nil {
		return fmt.Errorf("%s node: %w", nodeType, err)
	}
	return nil
}

func validatePath(base *core.NodeBase, nodeType, port string, path state.Path) error {
	if err := validateBase(base, nodeType); err != nil {
		return err
	}
	if path.Empty() {
		return fmt.Errorf("%s node %q requires resolved state port %q", nodeType, base.ID(), port)
	}
	return nil
}

func validatePathPair(base *core.NodeBase, nodeType string, firstName string, first state.Path, secondName string, second state.Path) error {
	if err := validatePath(base, nodeType, firstName, first); err != nil {
		return err
	}
	if second.Empty() {
		return fmt.Errorf("%s node %q requires resolved state port %q", nodeType, base.ID(), secondName)
	}
	return nil
}

func graphNodeSpec(base core.NodeBase, nodeType string, config map[string]any, paths map[string]state.Path) dsl.GraphNodeSpec {
	bindings := make(map[string]dsl.StateBinding, len(paths))
	for name, path := range paths {
		if !path.Empty() {
			bindings[name] = dsl.StateBinding{Path: path.String()}
		}
	}
	if len(bindings) == 0 {
		bindings = nil
	}
	if len(config) == 0 {
		config = nil
	}
	name := base.Name()
	if strings.TrimSpace(name) == "" {
		name = base.ID()
	}
	return dsl.GraphNodeSpec{
		ID:          base.ID(),
		Name:        name,
		Type:        nodeType,
		Description: base.Description(),
		Config:      config,
		State:       bindings,
	}
}

func applyMetadata(base *core.NodeBase, spec dsl.GraphNodeSpec) {
	base.Spec.ID = spec.ID
	if strings.TrimSpace(spec.Name) != "" {
		base.Spec.Name = spec.Name
	}
	if strings.TrimSpace(spec.Description) != "" {
		base.Spec.Description = spec.Description
	}
}

func resolvedPath(spec registry.ResolvedNodeSpec, name string) (state.Path, error) {
	binding, ok := spec.State[name]
	if !ok || binding.Path.Empty() {
		return state.Path{}, fmt.Errorf("node %q requires resolved state port %q", spec.Spec.ID, name)
	}
	return binding.Path, nil
}

func validateConfigKeys(nodeType, nodeID string, config map[string]any, allowed ...string) error {
	allowedKeys := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedKeys[key] = struct{}{}
	}
	for key := range config {
		if _, ok := allowedKeys[key]; !ok {
			return fmt.Errorf("build %s node %q: unknown config field %q", nodeType, nodeID, key)
		}
	}
	return nil
}

func normalizeJSON(value any) (any, int, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, 0, fmt.Errorf("value is not JSON compatible: %w", err)
	}
	var normalized any
	if err := json.Unmarshal(payload, &normalized); err != nil {
		return nil, 0, fmt.Errorf("normalize JSON value: %w", err)
	}
	return normalized, len(payload), nil
}

func readRequired(access *state.Access, path state.Path, nodeType, nodeID, port string) (any, error) {
	value, ok := access.ReadAny(path)
	if !ok {
		return nil, fmt.Errorf("%s node %q required state port %q path %q is missing", nodeType, nodeID, port, path.String())
	}
	return value, nil
}

func anyJSONSchema() dsl.JSONSchema {
	return dsl.JSONSchema{"title": "Any JSON value"}
}

func anyJSONConfigSchema(title, description string) dsl.JSONSchema {
	return dsl.JSONSchema{
		"type":        []any{"null", "boolean", "number", "string", "array", "object"},
		"title":       title,
		"description": description,
		"x-control":   "json",
		"default":     nil,
	}
}

func primitivePort(name, description string, schema dsl.JSONSchema, mode dsl.StateAccessMode, merge dsl.StateMergeStrategy) dsl.StatePortDefinition {
	return dsl.StatePortDefinition{
		Name:          name,
		Description:   description,
		Required:      true,
		Schema:        schema,
		Mode:          mode,
		MergeStrategy: merge,
	}
}

func replaceContract(path state.Path, mode state.AccessMode, required bool, description string) state.Contract {
	if path.Empty() {
		return state.Contract{}
	}
	return state.NewContract(state.FieldAccess{
		Path: path, Mode: mode, Required: required, Merge: state.MergeReplace, Description: description,
	})
}

func twoPathContract(source, target state.Path, targetMerge state.MergeStrategy, sourceDescription, targetDescription string) state.Contract {
	if source.Empty() || target.Empty() {
		return state.Contract{}
	}
	if source.String() == target.String() && targetMerge == state.MergeReplace {
		return state.NewContract(state.FieldAccess{
			Path: source, Mode: state.AccessReadWrite, Required: true, Merge: state.MergeReplace,
			Description: sourceDescription + " " + targetDescription,
		})
	}
	return state.NewContract(
		state.FieldAccess{Path: source, Mode: state.AccessRead, Required: true, Merge: state.MergeReplace, Description: sourceDescription},
		state.FieldAccess{Path: target, Mode: state.AccessWrite, Merge: targetMerge, Description: targetDescription},
	)
}
