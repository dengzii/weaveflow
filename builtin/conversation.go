package builtin

import (
	"fmt"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/registry"
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
