package weaveflow

import (
	"fmt"
	"strings"
	"weaveflow/dsl"
	"weaveflow/nodes"
	fruntime "weaveflow/runtime"
)

func RegisterSessionBootstrapModule(registry *Registry) {
	if registry == nil {
		return
	}

	registry.RegisterStateField(requestStateFieldDefinition())
	registry.RegisterStateField(agentStateFieldDefinition())
	registry.RegisterStateField(toolPolicyStateFieldDefinition())
	registry.RegisterNodeType(sessionBootstrapNodeTypeDefinition())
}

func requestStateFieldDefinition() StateFieldDefinition {
	return StateFieldDefinition{
		Name:        fruntime.StateKeyRequest,
		Description: "Normalized request input and metadata for the current agent run.",
		Schema: JSONSchema{
			"type": "object",
			"properties": JSONSchema{
				"input":    JSONSchema{"type": "string"},
				"metadata": JSONSchema{"type": "object"},
			},
			"additionalProperties": true,
		},
	}
}

func agentStateFieldDefinition() StateFieldDefinition {
	return StateFieldDefinition{
		Name:        fruntime.StateKeyAgent,
		Description: "Agent profile and runtime-level agent configuration.",
		Schema: JSONSchema{
			"type": "object",
			"properties": JSONSchema{
				"profile": JSONSchema{"type": "object"},
			},
			"additionalProperties": true,
		},
	}
}

func toolPolicyStateFieldDefinition() StateFieldDefinition {
	return StateFieldDefinition{
		Name:        fruntime.StateKeyToolPolicy,
		Description: "Tool availability and safety policy for the current agent run.",
		Schema: JSONSchema{
			"type":                 "object",
			"additionalProperties": true,
		},
	}
}

func sessionBootstrapNodeTypeDefinition() NodeTypeDefinition {
	return NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "session_bootstrap",
			Title:       "Session Bootstrap Node",
			Description: "Initialize request, agent, tool policy, and scoped conversation state for an agent run.",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"state_scope":      JSONSchema{"type": "string"},
					"input":            JSONSchema{"type": "string"},
					"input_path":       JSONSchema{"type": "string"},
					"system_prompt":    JSONSchema{"type": "string"},
					"max_iterations":   JSONSchema{"type": "integer", "minimum": 1},
					"agent_profile":    JSONSchema{"type": "object"},
					"request_metadata": JSONSchema{"type": "object"},
					"tool_policy":      JSONSchema{"type": "object"},
				},
				"additionalProperties": false,
			},
		},
		Build: func(ctx *BuildContext, spec dsl.GraphNodeSpec) (nodes.Node[State], error) {
			_ = ctx

			node := nodes.NewSessionBootstrapNode()
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			node.StateScope = stringConfig(spec.Config, "state_scope")
			node.Input = stringConfig(spec.Config, "input")
			node.InputPath = stringConfig(spec.Config, "input_path")
			node.SystemPrompt = stringConfig(spec.Config, "system_prompt")
			if value, ok := intConfig(spec.Config, "max_iterations"); ok {
				if value <= 0 {
					return nil, fmt.Errorf("build session_bootstrap node %q: max_iterations must be greater than 0", spec.ID)
				}
				node.MaxIterations = value
			}
			node.AgentProfile = objectConfig(spec.Config, "agent_profile")
			node.RequestMetadata = objectConfig(spec.Config, "request_metadata")
			node.ToolPolicy = objectConfig(spec.Config, "tool_policy")
			return node, nil
		},
		ResolveStateContract: resolveSessionBootstrapStateContract,
	}
}

func resolveSessionBootstrapStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	scope := stringConfig(spec.Config, "state_scope")
	inputPath := strings.TrimSpace(stringConfig(spec.Config, "input_path"))

	contract := dsl.StateContract{}
	if inputPath != "" && inputPath != fruntime.StateKeyRequest+".input" {
		contract.Fields = append(contract.Fields, dsl.StateFieldRef{
			Path:        inputPath,
			Mode:        dsl.StateAccessRead,
			Required:    true,
			Description: "Raw user input read by session bootstrap.",
		})
	}

	contract.Fields = append(contract.Fields,
		dsl.StateFieldRef{
			Path:        scopedConversationPath(scope, "messages"),
			Mode:        dsl.StateAccessReadWrite,
			Description: "Conversation messages initialized for the configured scope.",
		},
		dsl.StateFieldRef{
			Path:        scopedConversationPath(scope, "max_iterations"),
			Mode:        dsl.StateAccessWrite,
			Description: "Maximum iteration count initialized for the configured scope.",
		},
		dsl.StateFieldRef{
			Path:          fruntime.StateKeyRequest + ".input",
			Mode:          dsl.StateAccessReadWrite,
			Required:      true,
			Description:   "Normalized raw request input.",
			MergeStrategy: dsl.StateMergeMerge,
		},
		dsl.StateFieldRef{
			Path:          fruntime.StateKeyRequest + ".metadata",
			Mode:          dsl.StateAccessWrite,
			Description:   "Request metadata such as workspace, tenant, or user identifiers.",
			MergeStrategy: dsl.StateMergeMerge,
		},
		dsl.StateFieldRef{
			Path:          fruntime.StateKeyAgent + ".profile",
			Mode:          dsl.StateAccessWrite,
			Description:   "Agent profile made available to downstream nodes.",
			MergeStrategy: dsl.StateMergeMerge,
		},
		dsl.StateFieldRef{
			Path:          fruntime.StateKeyToolPolicy,
			Mode:          dsl.StateAccessWrite,
			Description:   "Tool policy made available to downstream guard and tool nodes.",
			MergeStrategy: dsl.StateMergeMerge,
		},
	)
	return contract, nil
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
		return cloneObjectConfigMap(typed)
	case State:
		return cloneObjectConfigMap(typed)
	default:
		return nil
	}
}

func cloneObjectConfigMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = cloneObjectConfigValue(value)
	}
	return cloned
}

func cloneObjectConfigValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneObjectConfigMap(typed)
	case State:
		return State(cloneObjectConfigMap(typed))
	case []any:
		cloned := make([]any, len(typed))
		for i, item := range typed {
			cloned[i] = cloneObjectConfigValue(item)
		}
		return cloned
	case []string:
		cloned := make([]string, len(typed))
		copy(cloned, typed)
		return cloned
	default:
		return value
	}
}
