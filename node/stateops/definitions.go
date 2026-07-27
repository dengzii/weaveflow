package stateops

import (
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/stateexpr"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

func NodeTypeDefinitions() []registry.NodeTypeDefinition {
	return []registry.NodeTypeDefinition{
		StateSetNodeTypeDefinition(),
		StateCopyNodeTypeDefinition(),
		StateDeleteNodeTypeDefinition(),
		StateMergeNodeTypeDefinition(),
		StateAppendNodeTypeDefinition(),
		StateTransformNodeTypeDefinition(),
	}
}

func StateSetNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        NodeTypeStateSet,
			Title:       "State Set",
			Description: "Set a JSON value at an explicitly bound state path.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"value": anyJSONConfigSchema("Value", "JSON value to write, including explicit null."),
				},
				"required":             []string{"value"},
				"additionalProperties": false,
			},
		},
		StatePorts: []dsl.StatePortDefinition{
			primitivePort("target", "State path to replace.", anyJSONSchema(), dsl.StateAccessWrite, dsl.StateMergeReplace),
		},
		Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (core.Node, error) {
			spec := resolved.Spec
			if err := validateConfigKeys(NodeTypeStateSet, spec.ID, spec.Config, "value"); err != nil {
				return nil, err
			}
			value, exists := spec.Config["value"]
			if !exists {
				return nil, fmt.Errorf("build %s node %q: config field %q is required", NodeTypeStateSet, spec.ID, "value")
			}
			normalized, _, err := normalizeJSON(value)
			if err != nil {
				return nil, fmt.Errorf("build %s node %q: config value: %w", NodeTypeStateSet, spec.ID, err)
			}
			target, err := resolvedPath(resolved, "target")
			if err != nil {
				return nil, err
			}
			node := NewStateSetNode(normalized, core.WithID(spec.ID))
			applyMetadata(&node.NodeBase, spec)
			node.TargetPath = target
			return node, nil
		},
	}
}

func StateCopyNodeTypeDefinition() registry.NodeTypeDefinition {
	return noConfigTwoPathDefinition(
		NodeTypeStateCopy,
		"State Copy",
		"Copy a value between explicitly bound state paths.",
		primitivePort("source", "State value to read.", anyJSONSchema(), dsl.StateAccessRead, dsl.StateMergeReplace),
		primitivePort("target", "State value to replace.", anyJSONSchema(), dsl.StateAccessWrite, dsl.StateMergeReplace),
		func(spec dsl.GraphNodeSpec, source, target registry.ResolvedStateBinding) core.Node {
			node := NewStateCopyNode(core.WithID(spec.ID))
			applyMetadata(&node.NodeBase, spec)
			node.SourcePath = source.Path
			node.TargetPath = target.Path
			return node
		},
	)
}

func StateDeleteNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type: NodeTypeStateDelete, Title: "State Delete",
			Description:  "Delete an explicitly bound state path.",
			ConfigSchema: emptyConfigSchema(),
		},
		StatePorts: []dsl.StatePortDefinition{
			primitivePort("target", "State path to delete.", anyJSONSchema(), dsl.StateAccessWrite, dsl.StateMergeReplace),
		},
		Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (core.Node, error) {
			spec := resolved.Spec
			if err := validateConfigKeys(NodeTypeStateDelete, spec.ID, spec.Config); err != nil {
				return nil, err
			}
			target, err := resolvedPath(resolved, "target")
			if err != nil {
				return nil, err
			}
			node := NewStateDeleteNode(core.WithID(spec.ID))
			applyMetadata(&node.NodeBase, spec)
			node.TargetPath = target
			return node, nil
		},
	}
}

func StateMergeNodeTypeDefinition() registry.NodeTypeDefinition {
	objectSchema := dsl.JSONSchema{"type": "object"}
	return noConfigTwoPathDefinition(
		NodeTypeStateMerge,
		"State Merge",
		"Deep-merge a bound JSON object into another bound state path.",
		primitivePort("source", "JSON object to read.", objectSchema, dsl.StateAccessRead, dsl.StateMergeReplace),
		primitivePort("target", "JSON object to merge.", objectSchema, dsl.StateAccessWrite, dsl.StateMergeMerge),
		func(spec dsl.GraphNodeSpec, source, target registry.ResolvedStateBinding) core.Node {
			node := NewStateMergeNode(core.WithID(spec.ID))
			applyMetadata(&node.NodeBase, spec)
			node.SourcePath = source.Path
			node.TargetPath = target.Path
			return node
		},
	)
}

func StateAppendNodeTypeDefinition() registry.NodeTypeDefinition {
	return noConfigTwoPathDefinition(
		NodeTypeStateAppend,
		"State Append",
		"Append a bound JSON value or array to a bound state array.",
		primitivePort("source", "JSON value or array to read.", anyJSONSchema(), dsl.StateAccessRead, dsl.StateMergeReplace),
		primitivePort("target", "State array to append.", dsl.JSONSchema{"type": "array"}, dsl.StateAccessWrite, dsl.StateMergeAppend),
		func(spec dsl.GraphNodeSpec, source, target registry.ResolvedStateBinding) core.Node {
			node := NewStateAppendNode(core.WithID(spec.ID))
			applyMetadata(&node.NodeBase, spec)
			node.SourcePath = source.Path
			node.TargetPath = target.Path
			return node
		},
	)
}

func StateTransformNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        NodeTypeStateTransform,
			Title:       "State Transform",
			Description: "Combine explicitly bound state inputs with deterministic CEL and write the result to another bound path.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"expression": dsl.JSONSchema{
						"type": "string", "title": "CEL Expression", "x-control": "textarea",
						"description": "Restricted CEL expression. Use inputs.<alias>; legacy graphs may continue using input.",
					},
				},
				"required":             []string{"expression"},
				"additionalProperties": false,
			},
			DynamicStatePorts: &dsl.DynamicStatePortDefinition{
				Description:   "JSON value exposed to CEL as inputs.<alias>.",
				NamePattern:   stateexpr.InputAliasPattern,
				Schema:        anyJSONSchema(),
				Mode:          dsl.StateAccessRead,
				MergeStrategy: dsl.StateMergeReplace,
			},
		},
		StatePorts: []dsl.StatePortDefinition{
			{Name: "input", Description: "Legacy JSON value exposed to the expression as input.", Schema: anyJSONSchema(), Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace},
			primitivePort("output", "Transformed JSON value to replace.", anyJSONSchema(), dsl.StateAccessWrite, dsl.StateMergeReplace),
		},
		Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (core.Node, error) {
			spec := resolved.Spec
			if err := validateConfigKeys(NodeTypeStateTransform, spec.ID, spec.Config, "expression"); err != nil {
				return nil, err
			}
			rawExpression, exists := spec.Config["expression"]
			if !exists {
				return nil, fmt.Errorf("build %s node %q: config field %q is required", NodeTypeStateTransform, spec.ID, "expression")
			}
			expression, ok := rawExpression.(string)
			if !ok || strings.TrimSpace(expression) == "" {
				return nil, fmt.Errorf("build %s node %q: config field %q must be a non-empty string", NodeTypeStateTransform, spec.ID, "expression")
			}
			output, err := resolvedPath(resolved, "output")
			if err != nil {
				return nil, err
			}
			node, err := NewStateTransformNode(expression, core.WithID(spec.ID))
			if err != nil {
				return nil, fmt.Errorf("build %s node %q: %w", NodeTypeStateTransform, spec.ID, err)
			}
			applyMetadata(&node.NodeBase, spec)
			if input, ok := resolved.State["input"]; ok {
				node.InputPath = input.Path
			}
			node.InputPaths = map[string]state.Path{}
			for name, binding := range resolved.State {
				if name == "input" || name == "output" {
					continue
				}
				node.InputPaths[name] = binding.Path
			}
			if node.InputPath.Empty() && len(node.InputPaths) == 0 {
				return nil, fmt.Errorf("build %s node %q: requires legacy state port %q or at least one dynamic input", NodeTypeStateTransform, spec.ID, "input")
			}
			node.OutputPath = output
			return node, nil
		},
	}
}

func noConfigTwoPathDefinition(
	nodeType, title, description string,
	sourcePort, targetPort dsl.StatePortDefinition,
	build func(dsl.GraphNodeSpec, registry.ResolvedStateBinding, registry.ResolvedStateBinding) core.Node,
) registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type: nodeType, Title: title, Description: description, ConfigSchema: emptyConfigSchema(),
		},
		StatePorts: []dsl.StatePortDefinition{sourcePort, targetPort},
		Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (core.Node, error) {
			spec := resolved.Spec
			if err := validateConfigKeys(nodeType, spec.ID, spec.Config); err != nil {
				return nil, err
			}
			source, ok := resolved.State["source"]
			if !ok || source.Path.Empty() {
				return nil, fmt.Errorf("node %q requires resolved state port %q", spec.ID, "source")
			}
			target, ok := resolved.State["target"]
			if !ok || target.Path.Empty() {
				return nil, fmt.Errorf("node %q requires resolved state port %q", spec.ID, "target")
			}
			return build(spec, source, target), nil
		},
	}
}

func emptyConfigSchema() dsl.JSONSchema {
	return dsl.JSONSchema{"type": "object", "properties": dsl.JSONSchema{}, "additionalProperties": false}
}
