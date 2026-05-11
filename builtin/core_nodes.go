package builtin

import (
	"fmt"
	"strconv"
	"strings"

	"weaveflow/core"
	"weaveflow/dsl"
	"weaveflow/nodes"
	"weaveflow/registry"
)

func RegisterCoreNodeTypes(r *registry.Registry) {
	if r == nil {
		return
	}

	r.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "subgraph",
			Title:       "Subgraph Node",
			Description: "Invoke another graph resolved by graph_ref using the current state.",
			ConfigSchema: dsl.JSONSchema{
				"type":                 "object",
				"properties":           dsl.JSONSchema{"graph_ref": dsl.JSONSchema{"type": "string"}},
				"required":             []string{"graph_ref"},
				"additionalProperties": false,
			},
		},
		ResolveStateContract: registry.ResolveSubgraphStateContract,
		Build: func(ctx registry.NodeBuildContext, spec dsl.GraphNodeSpec) (core.Node[registry.State], error) {
			graphRef := stringConfig(spec.Config, "graph_ref")
			if graphRef == "" {
				return nil, fmt.Errorf("build subgraph nodes %q: graph_ref is required", spec.ID)
			}
			options := ctx.BuildOptions()
			if options.SubgraphBuilder == nil {
				return nil, fmt.Errorf("build subgraph nodes %q: subgraph builder is required", spec.ID)
			}
			runner, err := options.SubgraphBuilder(graphRef)
			if err != nil {
				return nil, fmt.Errorf("build subgraph nodes %q: %w", spec.ID, err)
			}
			node := nodes.NewSubgraphNode()
			applyNodeMetadata(node, spec)
			node.GraphRef = graphRef
			node.InvokeSubgraph = runner
			return node, nil
		},
	})

	r.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "mapped_subgraph",
			Title:       "Mapped Subgraph Node",
			Description: "Invoke another graph with explicit input/output state path mappings.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"graph_ref":  dsl.JSONSchema{"type": "string"},
					"input_map":  dsl.JSONSchema{"type": "object", "additionalProperties": dsl.JSONSchema{"type": "string"}},
					"output_map": dsl.JSONSchema{"type": "object", "additionalProperties": dsl.JSONSchema{"type": "string"}},
				},
				"required":             []string{"graph_ref"},
				"additionalProperties": false,
			},
		},
		ResolveStateContract: registry.ResolveMappedSubgraphStateContract,
		Build: func(ctx registry.NodeBuildContext, spec dsl.GraphNodeSpec) (core.Node[registry.State], error) {
			graphRef := stringConfig(spec.Config, "graph_ref")
			if graphRef == "" {
				return nil, fmt.Errorf("build mapped_subgraph node %q: graph_ref is required", spec.ID)
			}
			options := ctx.BuildOptions()
			if options.SubgraphBuilder == nil {
				return nil, fmt.Errorf("build mapped_subgraph node %q: subgraph builder is required", spec.ID)
			}
			runner, err := options.SubgraphBuilder(graphRef)
			if err != nil {
				return nil, fmt.Errorf("build mapped_subgraph node %q: %w", spec.ID, err)
			}
			node := nodes.NewMappedSubgraphNode()
			applyNodeMetadata(node, spec)
			node.GraphRef = graphRef
			node.InputMap = mapStringConfig(spec.Config, "input_map")
			node.OutputMap = mapStringConfig(spec.Config, "output_map")
			node.InvokeSubgraph = runner
			return node, nil
		},
	})

	r.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "iterator",
			Title:       "Iterator Node",
			Description: "Iterate over a state array and inject the current iteration into temporary state.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"state_key":      dsl.JSONSchema{"type": "string"},
					"max_iterations": dsl.JSONSchema{"type": "integer", "minimum": 1},
					"continue_to":    dsl.JSONSchema{"type": "string"},
					"done_to":        dsl.JSONSchema{"type": "string"},
				},
				"required":             []string{"state_key", "max_iterations"},
				"additionalProperties": false,
			},
		},
		ResolveStateContract: registry.ResolveIteratorStateContract,
		Build: func(ctx registry.NodeBuildContext, spec dsl.GraphNodeSpec) (core.Node[registry.State], error) {
			_ = ctx
			stateKey := stringConfig(spec.Config, "state_key")
			if stateKey == "" {
				return nil, fmt.Errorf("build iterator nodes %q: state_key is required", spec.ID)
			}
			maxIterations, ok := intConfig(spec.Config, "max_iterations")
			if !ok || maxIterations <= 0 {
				return nil, fmt.Errorf("build iterator nodes %q: max_iterations must be greater than 0", spec.ID)
			}
			node := nodes.NewIteratorNode()
			applyNodeMetadata(node, spec)
			node.StateKey = stateKey
			node.MaxIterations = maxIterations
			node.ContinueTo = stringConfig(spec.Config, "continue_to")
			node.DoneTo = stringConfig(spec.Config, "done_to")
			return node, nil
		},
	})

	r.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "human_message",
			Title:       "Human Message Node",
			Description: "Pause the graph until the latest message in scope is a human message.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"state_scope":       dsl.JSONSchema{"type": "string"},
					"interrupt_message": dsl.JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
		ResolveStateContract: registry.ResolveHumanMessageStateContract,
		Build: func(ctx registry.NodeBuildContext, spec dsl.GraphNodeSpec) (core.Node[registry.State], error) {
			_ = ctx
			node := nodes.NewHumanMessageNode()
			applyNodeMetadata(node, spec)
			node.StateScope = stringConfig(spec.Config, "state_scope")
			node.InterruptMessage = stringConfig(spec.Config, "interrupt_message")
			return node, nil
		},
	})

	r.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "context_reducer",
			Title:       "Context Reducer Node",
			Description: "Compact older conversation context into a summary message before the next model turn.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"state_scope":     dsl.JSONSchema{"type": "string"},
					"max_messages":    dsl.JSONSchema{"type": "integer", "minimum": 2},
					"preserve_system": dsl.JSONSchema{"type": "boolean"},
					"preserve_recent": dsl.JSONSchema{"type": "integer", "minimum": 0},
					"summary_prefix":  dsl.JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
		ResolveStateContract: registry.ResolveContextReducerStateContract,
		Build: func(ctx registry.NodeBuildContext, spec dsl.GraphNodeSpec) (core.Node[registry.State], error) {
			_ = ctx
			node := nodes.NewContextReducerNode()
			applyNodeMetadata(node, spec)
			node.StateScope = stringConfig(spec.Config, "state_scope")
			node.MaxMessages, _ = intConfig(spec.Config, "max_messages")
			node.PreserveSystem, _ = boolConfig(spec.Config, "preserve_system")
			node.PreserveRecent, _ = intConfig(spec.Config, "preserve_recent")
			node.SummaryPrefix = stringConfig(spec.Config, "summary_prefix")
			return node, nil
		},
	})

	r.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "llm",
			Title:       "LLM Node",
			Description: "Built-in model inference nodes.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"tool_ids":         dsl.JSONSchema{"type": "array", "items": dsl.JSONSchema{"type": "string"}},
					"state_scope":      dsl.JSONSchema{"type": "string"},
					"prompt_max_chars": dsl.JSONSchema{"type": "integer", "minimum": 1},
				},
				"additionalProperties": false,
			},
		},
		ResolveStateContract: registry.ResolveLLMStateContract,
		Build: func(ctx registry.NodeBuildContext, spec dsl.GraphNodeSpec) (core.Node[registry.State], error) {
			_ = ctx
			node := nodes.NewLLMNode()
			applyNodeMetadata(node, spec)
			node.ToolIDs = stringSliceConfig(spec.Config, "tool_ids")
			node.StateScope = stringConfig(spec.Config, "state_scope")
			node.PromptMaxChars, _ = intConfig(spec.Config, "prompt_max_chars")
			return node, nil
		},
	})

	r.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "tools",
			Title:       "Tools Node",
			Description: "Built-in tool execution nodes.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"tool_ids":    dsl.JSONSchema{"type": "array", "items": dsl.JSONSchema{"type": "string"}},
					"state_scope": dsl.JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
		ResolveStateContract: registry.ResolveToolsStateContract,
		Build: func(ctx registry.NodeBuildContext, spec dsl.GraphNodeSpec) (core.Node[registry.State], error) {
			_ = ctx
			node := nodes.NewToolCallNode()
			applyNodeMetadata(node, spec)
			node.ToolIDs = stringSliceConfig(spec.Config, "tool_ids")
			node.StateScope = stringConfig(spec.Config, "state_scope")
			return node, nil
		},
	})

	r.RegisterCondition(registry.ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type:        "last_message_has_tool_calls",
			Title:       "Last Message Has Tool Calls",
			Description: "Routes when the last AI message includes tool calls.",
			ConfigSchema: dsl.JSONSchema{
				"type":                 "object",
				"properties":           dsl.JSONSchema{"state_scope": dsl.JSONSchema{"type": "string"}},
				"additionalProperties": false,
			},
		},
		Resolve: func(spec dsl.GraphConditionSpec) (registry.EdgeCondition, error) {
			return LastMessageHasToolCalls(stringConfig(spec.Config, "state_scope")), nil
		},
	})

	r.RegisterCondition(registry.ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type:        "has_final_answer",
			Title:       "Has Final Answer",
			Description: "Routes when the current state already contains a final answer.",
			ConfigSchema: dsl.JSONSchema{
				"type":                 "object",
				"properties":           dsl.JSONSchema{"state_scope": dsl.JSONSchema{"type": "string"}},
				"additionalProperties": false,
			},
		},
		Resolve: func(spec dsl.GraphConditionSpec) (registry.EdgeCondition, error) {
			return HasFinalAnswer(stringConfig(spec.Config, "state_scope")), nil
		},
	})

	r.RegisterCondition(registry.ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type:        "expression_conditions",
			Title:       "Expression Conditions",
			Description: "Routes by evaluating serializable expressions against the current state.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"state_scope": dsl.JSONSchema{"type": "string"},
					"match":       dsl.JSONSchema{"type": "string", "enum": []string{ExpressionMatchAll, ExpressionMatchAny}},
					"expressions": dsl.JSONSchema{
						"type": "array",
						"items": dsl.JSONSchema{
							"type": "object",
							"properties": dsl.JSONSchema{
								"value1": dsl.JSONSchema{"type": "string"},
								"op": dsl.JSONSchema{"type": "string", "enum": []string{
									OperationEqual,
									OperationNotEqual,
									OperationContains,
									OperationNotContain,
								}},
								"value2": dsl.JSONSchema{"type": "string"},
							},
							"required":             []string{"value1", "op", "value2"},
							"additionalProperties": false,
						},
					},
				},
				"required":             []string{"expressions"},
				"additionalProperties": false,
			},
		},
		Resolve: func(spec dsl.GraphConditionSpec) (registry.EdgeCondition, error) {
			config, err := ParseExpressionConditionConfig(spec.Config)
			if err != nil {
				return registry.EdgeCondition{}, fmt.Errorf("resolve expression condition: %w", err)
			}
			return ExpressionConditions(config)
		},
	})
}

func applyNodeMetadata(node interface {
	ID() string
	Name() string
	Description() string
}, spec dsl.GraphNodeSpec) {
	switch typed := node.(type) {
	case *nodes.SubgraphNode:
		typed.NodeID = spec.ID
		if spec.Name != "" {
			typed.NodeName = spec.Name
		}
		if spec.Description != "" {
			typed.NodeDescription = spec.Description
		}
	case *nodes.MappedSubgraphNode:
		typed.NodeID = spec.ID
		if spec.Name != "" {
			typed.NodeName = spec.Name
		}
		if spec.Description != "" {
			typed.NodeDescription = spec.Description
		}
	case *nodes.IteratorNode:
		typed.NodeID = spec.ID
		if spec.Name != "" {
			typed.NodeName = spec.Name
		}
		if spec.Description != "" {
			typed.NodeDescription = spec.Description
		}
	case *nodes.HumanMessageNode:
		typed.NodeID = spec.ID
		if spec.Name != "" {
			typed.NodeName = spec.Name
		}
		if spec.Description != "" {
			typed.NodeDescription = spec.Description
		}
	case *nodes.ContextReducerNode:
		typed.NodeID = spec.ID
		if spec.Name != "" {
			typed.NodeName = spec.Name
		}
		if spec.Description != "" {
			typed.NodeDescription = spec.Description
		}
	case *nodes.LLMNode:
		typed.NodeID = spec.ID
		if spec.Name != "" {
			typed.NodeName = spec.Name
		}
		if spec.Description != "" {
			typed.NodeDescription = spec.Description
		}
	case *nodes.ToolsNode:
		typed.NodeID = spec.ID
		if spec.Name != "" {
			typed.NodeName = spec.Name
		}
		if spec.Description != "" {
			typed.NodeDescription = spec.Description
		}
	}
}

func stringSliceConfig(config map[string]any, key string) []string {
	if len(config) == 0 {
		return nil
	}
	raw, ok := config[key]
	if !ok {
		return nil
	}
	values, ok := raw.([]any)
	if ok {
		result := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				result = append(result, text)
			}
		}
		return result
	}
	if typed, ok := raw.([]string); ok {
		return append([]string(nil), typed...)
	}
	return nil
}

func mapStringConfig(config map[string]any, key string) map[string]string {
	if len(config) == 0 {
		return nil
	}
	raw, ok := config[key]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case map[string]string:
		result := make(map[string]string, len(typed))
		for k, v := range typed {
			result[k] = v
		}
		return result
	case map[string]any:
		result := make(map[string]string, len(typed))
		for k, v := range typed {
			if s, ok := v.(string); ok {
				result[k] = s
			}
		}
		return result
	}
	return nil
}

func stringConfig(config map[string]any, key string) string {
	if len(config) == 0 {
		return ""
	}
	if value, ok := config[key].(string); ok {
		return value
	}
	return ""
}

func intConfig(config map[string]any, key string) (int, bool) {
	if len(config) == 0 {
		return 0, false
	}
	switch value := config[key].(type) {
	case int:
		return value, true
	case int8:
		return int(value), true
	case int16:
		return int(value), true
	case int32:
		return int(value), true
	case int64:
		return int(value), true
	case float32:
		return int(value), true
	case float64:
		return int(value), true
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func boolConfig(config map[string]any, key string) (bool, bool) {
	if len(config) == 0 {
		return false, false
	}
	switch value := config[key].(type) {
	case bool:
		return value, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		if err == nil {
			return parsed, true
		}
	}
	return false, false
}
