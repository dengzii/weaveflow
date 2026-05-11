package conversation

import (
	"fmt"
	"strings"
	"weaveflow/core"
	"weaveflow/dsl"
	"weaveflow/nodes"
	"weaveflow/registry"
	fruntime "weaveflow/runtime"
)

type legacyNodeBuilder func(*registry.BuildContext, dsl.GraphNodeSpec) (core.Node[fruntime.State], error)

func adaptLegacyNodeBuilder(build legacyNodeBuilder) func(registry.NodeBuildContext, dsl.GraphNodeSpec) (core.Node[fruntime.State], error) {
	return func(ctx registry.NodeBuildContext, spec dsl.GraphNodeSpec) (core.Node[fruntime.State], error) {
		if build == nil {
			return nil, fmt.Errorf("node builder is nil")
		}
		if concrete, ok := ctx.(*registry.BuildContext); ok {
			return build(concrete, spec)
		}
		if ctx == nil {
			return build(nil, spec)
		}
		options := ctx.BuildOptions()
		return build(&registry.BuildContext{
			InstanceConfig:       options.InstanceConfig,
			GraphResolver:        options.GraphResolver,
			OnContractDiagnostic: options.OnContractDiagnostic,
		}, spec)
	}
}

func Register(registry *registry.Registry) {
	if registry == nil {
		return
	}
	registerSessionBootstrapModule(registry)
	registerContextModule(registry)
}

func registerSessionBootstrapModule(registry *registry.Registry) {
	registry.RegisterStateField(requestStateFieldDefinition())
	registry.RegisterStateField(agentStateFieldDefinition())
	registry.RegisterStateField(toolPolicyStateFieldDefinition())
	registry.RegisterNodeType(sessionBootstrapNodeTypeDefinition())
}

func requestStateFieldDefinition() dsl.StateFieldDefinition {
	return dsl.StateFieldDefinition{
		Name:        fruntime.StateKeyRequest,
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
		Name:        fruntime.StateKeyAgent,
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
		Name:        fruntime.StateKeyToolPolicy,
		Description: "Tool availability and safety policy for the current agent run.",
		Schema: dsl.JSONSchema{
			"type":                 "object",
			"additionalProperties": true,
		},
	}
}

func sessionBootstrapNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "session_bootstrap",
			Title:       "Session Bootstrap Node",
			Description: "Initialize request, agent, tool policy, and scoped conversation state for an agent run.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"state_scope":      dsl.JSONSchema{"type": "string"},
					"input":            dsl.JSONSchema{"type": "string"},
					"input_path":       dsl.JSONSchema{"type": "string"},
					"system_prompt":    dsl.JSONSchema{"type": "string"},
					"max_iterations":   dsl.JSONSchema{"type": "integer", "minimum": 1},
					"agent_profile":    dsl.JSONSchema{"type": "object"},
					"request_metadata": dsl.JSONSchema{"type": "object"},
					"tool_policy":      dsl.JSONSchema{"type": "object"},
				},
				"additionalProperties": false,
			},
		},
		Build: adaptLegacyNodeBuilder(func(ctx *registry.BuildContext, spec dsl.GraphNodeSpec) (core.Node[fruntime.State], error) {
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
		}),
		ResolveStateContract: resolveSessionBootstrapStateContract,
	}
}

func registerContextModule(registry *registry.Registry) {
	registry.RegisterNodeType(contextAssemblerNodeTypeDefinition())
}

func contextAssemblerNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "context_assembler",
			Title:       "Context Assembler Node",
			Description: "Inject recalled memory into the scoped conversation before the next model turn.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"state_scope":              dsl.JSONSchema{"type": "string"},
					"memory_state_path":        dsl.JSONSchema{"type": "string"},
					"orchestration_state_path": dsl.JSONSchema{"type": "string"},
					"planner_state_path":       dsl.JSONSchema{"type": "string"},
					"include_memory":           dsl.JSONSchema{"type": "boolean"},
					"include_orchestration":    dsl.JSONSchema{"type": "boolean"},
					"include_planner":          dsl.JSONSchema{"type": "boolean"},
					"memory_heading":           dsl.JSONSchema{"type": "string"},
					"orchestration_heading":    dsl.JSONSchema{"type": "string"},
					"planner_heading":          dsl.JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
		Build: adaptLegacyNodeBuilder(func(ctx *registry.BuildContext, spec dsl.GraphNodeSpec) (core.Node[fruntime.State], error) {
			_ = ctx
			node := nodes.NewContextAssemblerNode()
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			node.StateScope = stringConfig(spec.Config, "state_scope")
			node.MemoryStatePath = stringConfig(spec.Config, "memory_state_path")
			node.OrchestrationStatePath = stringConfig(spec.Config, "orchestration_state_path")
			node.PlannerStatePath = stringConfig(spec.Config, "planner_state_path")
			node.MemoryHeading = stringConfig(spec.Config, "memory_heading")
			node.OrchestrationHeading = stringConfig(spec.Config, "orchestration_heading")
			node.PlannerHeading = stringConfig(spec.Config, "planner_heading")
			if value, ok := boolConfig(spec.Config, "include_memory"); ok {
				node.IncludeMemory = value
			}
			if value, ok := boolConfig(spec.Config, "include_orchestration"); ok {
				node.IncludeOrchestration = value
			}
			if value, ok := boolConfig(spec.Config, "include_planner"); ok {
				node.IncludePlanner = value
			}
			return node, nil
		}),
		ResolveStateContract: resolveContextAssemblerStateContract,
	}
}

func resolveContextAssemblerStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	scope := stringConfig(spec.Config, "state_scope")
	memoryPath := stringConfig(spec.Config, "memory_state_path")
	if strings.TrimSpace(memoryPath) == "" {
		memoryPath = fruntime.StateKeyMemory
	}
	memoryPath = canonicalContractPath(memoryPath)
	orchestrationPath := stringConfig(spec.Config, "orchestration_state_path")
	if strings.TrimSpace(orchestrationPath) == "" {
		orchestrationPath = fruntime.StateKeyOrchestration
	}
	orchestrationPath = canonicalContractPath(orchestrationPath)
	plannerPath := stringConfig(spec.Config, "planner_state_path")
	if strings.TrimSpace(plannerPath) == "" {
		plannerPath = fruntime.StateKeyPlanner
	}
	plannerPath = canonicalContractPath(plannerPath)

	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{
				Path:        scopedConversationPath(scope, "messages"),
				Mode:        dsl.StateAccessReadWrite,
				Description: "Conversation messages updated with assembled memory context.",
			},
			{
				Path:        memoryPath,
				Mode:        dsl.StateAccessRead,
				Description: "Structured memory state consumed for prompt assembly.",
			},
			{
				Path:        orchestrationPath,
				Mode:        dsl.StateAccessRead,
				Description: "Structured orchestration state consumed for prompt assembly.",
			},
			{
				Path:        plannerPath,
				Mode:        dsl.StateAccessRead,
				Description: "Structured planner state consumed for prompt assembly.",
			},
		},
	}, nil
}

func resolveSessionBootstrapStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	scope := stringConfig(spec.Config, "state_scope")
	inputPath := strings.TrimSpace(stringConfig(spec.Config, "input_path"))

	contract := dsl.StateContract{}
	if inputPath != "" && inputPath != fruntime.StateKeyRequest+".input" {
		contract.Fields = append(contract.Fields, dsl.StateFieldRef{
			Path:        canonicalContractPath(inputPath),
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
			Path:          canonicalContractPath(fruntime.StateKeyRequest + ".input"),
			Mode:          dsl.StateAccessReadWrite,
			Required:      true,
			Description:   "Normalized raw request input.",
			MergeStrategy: dsl.StateMergeMerge,
		},
		dsl.StateFieldRef{
			Path:          canonicalContractPath(fruntime.StateKeyRequest + ".metadata"),
			Mode:          dsl.StateAccessWrite,
			Description:   "Request metadata such as workspace, tenant, or user identifiers.",
			MergeStrategy: dsl.StateMergeMerge,
		},
		dsl.StateFieldRef{
			Path:          canonicalContractPath(fruntime.StateKeyAgent + ".profile"),
			Mode:          dsl.StateAccessWrite,
			Description:   "Agent profile made available to downstream nodes.",
			MergeStrategy: dsl.StateMergeMerge,
		},
		dsl.StateFieldRef{
			Path:          canonicalContractPath(fruntime.StateKeyToolPolicy),
			Mode:          dsl.StateAccessWrite,
			Description:   "Tool policy made available to downstream guard and tool nodes.",
			MergeStrategy: dsl.StateMergeMerge,
		},
	)
	return contract, nil
}

func stringConfig(config map[string]any, key string) string {
	if len(config) == 0 {
		return ""
	}
	value, _ := config[key].(string)
	return strings.TrimSpace(value)
}

func boolConfig(config map[string]any, key string) (bool, bool) {
	if len(config) == 0 {
		return false, false
	}
	value, ok := config[key].(bool)
	return value, ok
}

func intConfig(config map[string]any, key string) (int, bool) {
	if len(config) == 0 {
		return 0, false
	}
	switch typed := config[key].(type) {
	case int:
		return typed, true
	case int8:
		return int(typed), true
	case int16:
		return int(typed), true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case float32:
		return int(typed), true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
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
	case fruntime.State:
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
	case fruntime.State:
		return fruntime.State(cloneObjectConfigMap(typed))
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

func canonicalContractPath(path string) string {
	return fruntime.NormalizeContractPath(path)
}

func scopedConversationPath(scope string, field string) string {
	scope = strings.TrimSpace(scope)
	field = strings.TrimSpace(field)
	if field == "" {
		if scope == "" {
			return "conversation"
		}
		return "scopes." + scope
	}
	if scope == "" {
		return "conversation." + field
	}
	return "scopes." + scope + "." + field
}
