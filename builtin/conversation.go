package builtin

import (
	"fmt"
	"strings"

	"weaveflow/dsl"
	"weaveflow/node"
	"weaveflow/registry"
	"weaveflow/state"
	"weaveflow/state/accessors"
)

func registerConversationModule(registry *registry.Registry) {
	if registry == nil {
		return
	}
	registerSessionBootstrapModule(registry)
}

func registerSessionBootstrapModule(registry *registry.Registry) {
	registry.RegisterStateField(requestStateFieldDefinition())
	registry.RegisterStateField(agentStateFieldDefinition())
	registry.RegisterStateField(toolPolicyStateFieldDefinition())
	registry.RegisterStateField(environmentStateFieldDefinition())
	registry.RegisterNodeType(environmentContextNodeTypeDefinition())
}

func requestStateFieldDefinition() dsl.StateFieldDefinition {
	return dsl.StateFieldDefinition{
		Name:        accessors.KeyRequest,
		Description: "Normalized request input and metadata for the current agent run.",
		Schema: dsl.JSONSchema{
			"type": "object",
			"properties": dsl.JSONSchema{
				"input":    dsl.JSONSchema{"type": "string"},
				"metadata": dsl.JSONSchema{"type": "object"},
			},
			"additionalProperties": true,
		},
	}
}

func agentStateFieldDefinition() dsl.StateFieldDefinition {
	return dsl.StateFieldDefinition{
		Name:        accessors.KeyAgent,
		Description: "Agent profile and runtime-level agent configuration.",
		Schema: dsl.JSONSchema{
			"type": "object",
			"properties": dsl.JSONSchema{
				"profile": dsl.JSONSchema{"type": "object"},
			},
			"additionalProperties": true,
		},
	}
}

func toolPolicyStateFieldDefinition() dsl.StateFieldDefinition {
	return dsl.StateFieldDefinition{
		Name:        accessors.KeyToolPolicy,
		Description: "Tool availability and safety policy for the current agent run.",
		Schema: dsl.JSONSchema{
			"type":                 "object",
			"additionalProperties": true,
		},
	}
}

func environmentStateFieldDefinition() dsl.StateFieldDefinition {
	return dsl.StateFieldDefinition{
		Name:        accessors.KeyEnvironment,
		Description: "Workspace, project, and version-control context for the current agent run.",
		Schema: dsl.JSONSchema{
			"type": "object",
			"properties": dsl.JSONSchema{
				"workspace_root": dsl.JSONSchema{"type": "string"},
				"cwd":            dsl.JSONSchema{"type": "string"},
				"source":         dsl.JSONSchema{"type": "string"},
				"os":             dsl.JSONSchema{"type": "string"},
				"project":        dsl.JSONSchema{"type": "object"},
				"git":            dsl.JSONSchema{"type": "object"},
			},
			"additionalProperties": true,
		},
	}
}

func environmentContextNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        node.NodeTypeEnvironmentContext,
			Title:       "Environment Context Node",
			Description: "Collect workspace, project, and git context into shared state.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"environment_state_path": dsl.JSONSchema{"type": "string"},
					"workspace_root":         dsl.JSONSchema{"type": "string"},
					"include_git":            dsl.JSONSchema{"type": "boolean"},
					"include_project":        dsl.JSONSchema{"type": "boolean"},
					"git_status_limit":       dsl.JSONSchema{"type": "integer", "minimum": 1},
				},
				"additionalProperties": false,
			},
		},
		Build: func(ctx registry.NodeBuildContext, spec dsl.GraphNodeSpec) (node.Node, error) {
			_ = ctx
			node := node.NewEnvironmentContextNode(node.WithID(spec.ID))
			applyNodeMetadata(&node.Base, spec)
			node.EnvironmentStatePath = registry.StringConfigTrim(spec.Config, "environment_state_path")
			node.WorkspaceRoot = registry.StringConfigTrim(spec.Config, "workspace_root")
			if value, ok := registry.BoolConfig(spec.Config, "include_git"); ok {
				node.IncludeGit = value
			}
			if value, ok := registry.BoolConfig(spec.Config, "include_project"); ok {
				node.IncludeProject = value
			}
			if value, ok := registry.IntConfig(spec.Config, "git_status_limit"); ok {
				if value <= 0 {
					return nil, fmt.Errorf("build environment_context node %q: git_status_limit must be greater than 0", spec.ID)
				}
				node.GitStatusLimit = value
			}
			return node, nil
		},
		ResolveStateContract: resolveEnvironmentContextStateContract,
	}
}

func resolveEnvironmentContextStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	environmentPath := registry.StringConfigTrim(spec.Config, "environment_state_path")
	if strings.TrimSpace(environmentPath) == "" {
		environmentPath = state.Shared(accessors.KeyEnvironment).String()
	}
	parsed, err := state.ParsePath(environmentPath)
	if err != nil {
		return dsl.StateContract{}, err
	}
	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{
				Path:          parsed.String(),
				Mode:          dsl.StateAccessWrite,
				Description:   "Collected workspace, project, and git context.",
				MergeStrategy: dsl.StateMergeReplace,
			},
		},
	}, nil
}

func objectConfig(config map[string]any, key string) map[string]any {
	if len(config) == 0 {
		return nil
	}
	raw, ok := config[key]
	if !ok || raw == nil {
		return nil
	}
	switch typed := raw.(type) {
	case map[string]any:
		return registry.CloneMap(typed)
	default:
		return nil
	}
}
