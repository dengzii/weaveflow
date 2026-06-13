package builtin

import (
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

func RegisterCoreNodeTypes(r *registry.Registry) {
	if r == nil {
		return
	}

	r.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        node.NodeTypeMappedSubgraph,
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
		Build: func(ctx registry.NodeBuildContext, spec dsl.GraphNodeSpec) (node.Node, error) {
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
			node := node.NewMappedSubgraphNode(node.WithID(spec.ID))
			applyNodeMetadata(&node.Base, spec)
			node.GraphRef = graphRef
			node.InputMappings, err = parsePathMappings(registry.MapStringConfig(spec.Config, "input_map"), false)
			if err != nil {
				return nil, fmt.Errorf("build mapped_subgraph node %q input_map: %w", spec.ID, err)
			}
			node.OutputMappings, err = parsePathMappings(registry.MapStringConfig(spec.Config, "output_map"), true)
			if err != nil {
				return nil, fmt.Errorf("build mapped_subgraph node %q output_map: %w", spec.ID, err)
			}
			node.InvokeSubgraph = runner
			return node, nil
		},
	})

	r.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        node.NodeTypeHumanMessage,
			Title:       "Human Message Node",
			Description: "Pause the graph until the latest message in scope is a human message.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"state_scope":       dsl.JSONSchema{"type": "string"},
					"interrupt_message": dsl.JSONSchema{"type": "string"},
					"content":           dsl.JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
		ResolveStateContract: registry.ResolveHumanMessageStateContract,
		Build: func(ctx registry.NodeBuildContext, spec dsl.GraphNodeSpec) (node.Node, error) {
			_ = ctx
			node := node.NewHumanMessageNode(registry.StringConfig(spec.Config, "content"), node.WithScope(nodeStateScope(spec.Config)), node.WithID(spec.ID))
			applyNodeMetadata(&node.Base, spec)
			if value := registry.StringConfig(spec.Config, "interrupt_message"); value != "" {
				node.InterruptMessage = value
			}
			return node, nil
		},
	})

	r.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        node.NodeTypeContextReducer,
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
		Build: func(ctx registry.NodeBuildContext, spec dsl.GraphNodeSpec) (node.Node, error) {
			_ = ctx
			node := node.NewContextReducerNode(node.WithScope(nodeStateScope(spec.Config)), node.WithID(spec.ID))
			applyNodeMetadata(&node.Base, spec)
			node.MaxMessages, _ = registry.IntConfig(spec.Config, "max_messages")
			if value, ok := registry.BoolConfig(spec.Config, "preserve_system"); ok {
				node.PreserveSystem = value
			}
			node.PreserveRecent, _ = registry.IntConfig(spec.Config, "preserve_recent")
			if value := registry.StringConfig(spec.Config, "summary_prefix"); value != "" {
				node.SummaryPrefix = value
			}
			return node, nil
		},
	})

	r.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        node.NodeTypeLLM,
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
		Build: func(ctx registry.NodeBuildContext, spec dsl.GraphNodeSpec) (node.Node, error) {
			_ = ctx
			node := node.NewLLMNode(node.WithScope(nodeStateScope(spec.Config)), node.WithID(spec.ID))
			applyNodeMetadata(&node.Base, spec)
			node.ToolIDs = registry.StringSliceConfig(spec.Config, "tool_ids")
			node.PromptMaxChars, _ = registry.IntConfig(spec.Config, "prompt_max_chars")
			return node, nil
		},
	})

	r.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        node.NodeTypeTools,
			Title:       "Tools Node",
			Description: "Built-in tool execution nodes.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"tool_ids":    dsl.JSONSchema{"type": "array", "items": dsl.JSONSchema{"type": "string"}},
					"state_scope": dsl.JSONSchema{"type": "string"},
					"parallel":    dsl.JSONSchema{"type": "boolean"},
				},
				"additionalProperties": false,
			},
		},
		ResolveStateContract: registry.ResolveToolsStateContract,
		Build: func(ctx registry.NodeBuildContext, spec dsl.GraphNodeSpec) (node.Node, error) {
			_ = ctx
			node := node.NewToolsNode(node.WithScope(nodeStateScope(spec.Config)), node.WithID(spec.ID))
			applyNodeMetadata(&node.Base, spec)
			node.ToolIDs = registry.StringSliceConfig(spec.Config, "tool_ids")
			if parallel, ok := registry.BoolConfig(spec.Config, "parallel"); ok {
				node.Parallel = parallel
			}
			return node, nil
		},
	})

	r.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        node.NodeTypeAgent,
			Title:       "Agent Node",
			Description: "Run a self-contained ReAct loop: LLM inference and tool execution iterate inside the node until a final answer or the iteration cap is reached.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"tool_ids":         dsl.JSONSchema{"type": "array", "items": dsl.JSONSchema{"type": "string"}},
					"state_scope":      dsl.JSONSchema{"type": "string"},
					"system_prompt":    dsl.JSONSchema{"type": "string"},
					"input_path":       dsl.JSONSchema{"type": "string"},
					"output_path":      dsl.JSONSchema{"type": "string"},
					"max_iterations":   dsl.JSONSchema{"type": "integer", "minimum": 1},
					"prompt_max_chars": dsl.JSONSchema{"type": "integer", "minimum": 1},
					"parallel":         dsl.JSONSchema{"type": "boolean"},
					"tool_name":        dsl.JSONSchema{"type": "string"},
					"tool_description": dsl.JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
		ResolveStateContract: registry.ResolveAgentStateContract,
		Build: func(ctx registry.NodeBuildContext, spec dsl.GraphNodeSpec) (node.Node, error) {
			_ = ctx
			node := node.NewAgentNode(node.WithScope(nodeStateScope(spec.Config)), node.WithID(spec.ID))
			applyNodeMetadata(&node.Base, spec)
			node.ToolIDs = registry.StringSliceConfig(spec.Config, "tool_ids")
			node.SystemPrompt = registry.StringConfig(spec.Config, "system_prompt")
			var err error
			node.InputPath, err = parseOptionalStatePath(registry.StringConfig(spec.Config, "input_path"))
			if err != nil {
				return nil, fmt.Errorf("build agent node %q input_path: %w", spec.ID, err)
			}
			node.OutputPath, err = parseOptionalStatePath(registry.StringConfig(spec.Config, "output_path"))
			if err != nil {
				return nil, fmt.Errorf("build agent node %q output_path: %w", spec.ID, err)
			}
			node.MaxIterations, _ = registry.IntConfig(spec.Config, "max_iterations")
			node.PromptMaxChars, _ = registry.IntConfig(spec.Config, "prompt_max_chars")
			if parallel, ok := registry.BoolConfig(spec.Config, "parallel"); ok {
				node.Parallel = parallel
			}
			node.ToolName = registry.StringConfig(spec.Config, "tool_name")
			node.ToolDescription = registry.StringConfig(spec.Config, "tool_description")
			return node, nil
		},
	})

	registerCoreConditions(r)
}

func registerCoreConditions(r *registry.Registry) {
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
			return LastMessageHasToolCalls(conditionStateScope(spec.Config)), nil
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
			return HasFinalAnswer(conditionStateScope(spec.Config)), nil
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
								"logic": dsl.JSONSchema{"type": "string", "enum": []string{
									LogicAnd,
									LogicOr,
									LogicNot,
								}},
								"children": dsl.JSONSchema{
									"type":  "array",
									"items": dsl.JSONSchema{"type": "object"},
								},
							},
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

func conditionStateScope(config map[string]any) string {
	if _, ok := config["state_scope"]; ok {
		return registry.StringConfig(config, "state_scope")
	}
	return node.DefaultScope
}

func nodeStateScope(config map[string]any) string {
	if _, ok := config["state_scope"]; ok {
		return registry.StringConfig(config, "state_scope")
	}
	return node.DefaultScope
}

func applyNodeMetadata(base *node.Base, spec dsl.GraphNodeSpec) {
	if base == nil {
		return
	}
	base.Spec.ID = spec.ID
	if strings.TrimSpace(spec.Name) != "" {
		base.Spec.Name = spec.Name
	}
	if strings.TrimSpace(spec.Description) != "" {
		base.Spec.Description = spec.Description
	}
}

func parsePathMappings(values map[string]string, reverse bool) ([]node.PathMapping, error) {
	if len(values) == 0 {
		return nil, nil
	}
	mappings := make([]node.PathMapping, 0, len(values))
	for fromText, toText := range values {
		from, err := parseRequiredStatePath(fromText)
		if err != nil {
			return nil, err
		}
		to, err := parseRequiredStatePath(toText)
		if err != nil {
			return nil, err
		}
		if reverse {
			mappings = append(mappings, node.PathMapping{From: from, To: to})
			continue
		}
		mappings = append(mappings, node.PathMapping{From: from, To: to})
	}
	return mappings, nil
}

func parseOptionalStatePath(text string) (state.Path, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return state.Path{}, nil
	}
	return parseRequiredStatePath(text)
}

func parseRequiredStatePath(text string) (state.Path, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return state.Path{}, fmt.Errorf("state path is required")
	}
	return state.ParsePath(text)
}
