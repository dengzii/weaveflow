package builtin

import (
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
	"github.com/dengzii/weaveflow/state/accessors"
)

func registerConversationModule(registry *registry.Registry) error {
	if registry == nil {
		return fmt.Errorf("registry is nil")
	}
	return registerSessionBootstrapModule(registry)
}

func registerSessionBootstrapModule(registry *registry.Registry) error {
	for _, field := range []dsl.StateFieldDefinition{
		requestStateFieldDefinition(),
		agentStateFieldDefinition(),
		toolPolicyStateFieldDefinition(),
		environmentStateFieldDefinition(),
	} {
		if err := registry.RegisterStateField(field); err != nil {
			return fmt.Errorf("register state field %q: %w", field.Name, err)
		}
	}
	def := environmentContextNodeTypeDefinition()
	if err := registry.RegisterNodeType(def); err != nil {
		return fmt.Errorf("register node type %q: %w", def.Type, err)
	}
	return nil
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
		Build: func(ctx *registry.BuildContext, spec dsl.GraphNodeSpec) (node.Node, error) {
			_ = ctx
			envNode := node.NewEnvironmentContextNode(node.WithID(spec.ID))
			applyNodeMetadata(&envNode.Base, spec)
			envNode.EnvironmentStatePath = config.TrimmedString(spec.Config, "environment_state_path")
			envNode.WorkspaceRoot = config.TrimmedString(spec.Config, "workspace_root")
			if value, ok := config.Bool(spec.Config, "include_git"); ok {
				envNode.IncludeGit = value
			}
			if value, ok := config.Bool(spec.Config, "include_project"); ok {
				envNode.IncludeProject = value
			}
			if value, ok := config.Int(spec.Config, "git_status_limit"); ok {
				if value <= 0 {
					return nil, fmt.Errorf("build environment_context node %q: git_status_limit must be greater than 0", spec.ID)
				}
				envNode.GitStatusLimit = value
			}
			return envNode, nil
		},
		ResolveStateContract: resolveEnvironmentContextStateContract,
	}
}

func resolveEnvironmentContextStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	environmentPath := config.TrimmedString(spec.Config, "environment_state_path")
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

func objectConfig(configMap map[string]any, key string) map[string]any {
	if len(configMap) == 0 {
		return nil
	}
	raw, ok := configMap[key]
	if !ok || raw == nil {
		return nil
	}
	switch typed := raw.(type) {
	case map[string]any:
		return config.CloneMap(typed)
	default:
		return nil
	}
}
