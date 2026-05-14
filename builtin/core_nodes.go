package builtin

import (
	"fmt"

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
			graphRef := registry.StringConfig(spec.Config, "graph_ref")
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
			node.InputMap = registry.MapStringConfig(spec.Config, "input_map")
			node.OutputMap = registry.MapStringConfig(spec.Config, "output_map")
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
			stateKey := registry.StringConfig(spec.Config, "state_key")
			if stateKey == "" {
				return nil, fmt.Errorf("build iterator nodes %q: state_key is required", spec.ID)
			}
			maxIterations, ok := registry.IntConfig(spec.Config, "max_iterations")
			if !ok || maxIterations <= 0 {
				return nil, fmt.Errorf("build iterator nodes %q: max_iterations must be greater than 0", spec.ID)
			}
			node := nodes.NewIteratorNode()
			applyNodeMetadata(node, spec)
			node.StateKey = stateKey
			node.MaxIterations = maxIterations
			node.ContinueTo = registry.StringConfig(spec.Config, "continue_to")
			node.DoneTo = registry.StringConfig(spec.Config, "done_to")
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
			node.StateScope = registry.StringConfig(spec.Config, "state_scope")
			node.InterruptMessage = registry.StringConfig(spec.Config, "interrupt_message")
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
			node.StateScope = registry.StringConfig(spec.Config, "state_scope")
			node.MaxMessages, _ = registry.IntConfig(spec.Config, "max_messages")
			node.PreserveSystem, _ = registry.BoolConfig(spec.Config, "preserve_system")
			node.PreserveRecent, _ = registry.IntConfig(spec.Config, "preserve_recent")
			node.SummaryPrefix = registry.StringConfig(spec.Config, "summary_prefix")
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
			node.ToolIDs = registry.StringSliceConfig(spec.Config, "tool_ids")
			node.StateScope = registry.StringConfig(spec.Config, "state_scope")
			node.PromptMaxChars, _ = registry.IntConfig(spec.Config, "prompt_max_chars")
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
			node.ToolIDs = registry.StringSliceConfig(spec.Config, "tool_ids")
			node.StateScope = registry.StringConfig(spec.Config, "state_scope")
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
			return LastMessageHasToolCalls(registry.StringConfig(spec.Config, "state_scope")), nil
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
			return HasFinalAnswer(registry.StringConfig(spec.Config, "state_scope")), nil
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
