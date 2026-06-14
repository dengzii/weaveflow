package builtin

import (
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
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
		ResolveStateContract: resolveMappedSubgraphStateContract,
		Build: func(ctx *registry.BuildContext, spec dsl.GraphNodeSpec) (node.Node, error) {
			graphRef := config.String(spec.Config, "graph_ref")
			if graphRef == "" {
				return nil, fmt.Errorf("build mapped_subgraph node %q: graph_ref is required", spec.ID)
			}
			if ctx == nil || ctx.SubgraphBuilder == nil {
				return nil, fmt.Errorf("build mapped_subgraph node %q: subgraph builder is required", spec.ID)
			}
			runner, err := ctx.SubgraphBuilder(graphRef)
			if err != nil {
				return nil, fmt.Errorf("build mapped_subgraph node %q: %w", spec.ID, err)
			}
			mappedNode := node.NewMappedSubgraphNode(node.WithID(spec.ID))
			applyNodeMetadata(&mappedNode.Base, spec)
			mappedNode.GraphRef = graphRef
			mappedNode.InputMappings, err = parsePathMappings(config.StringMap(spec.Config, "input_map"), false)
			if err != nil {
				return nil, fmt.Errorf("build mapped_subgraph node %q input_map: %w", spec.ID, err)
			}
			mappedNode.OutputMappings, err = parsePathMappings(config.StringMap(spec.Config, "output_map"), true)
			if err != nil {
				return nil, fmt.Errorf("build mapped_subgraph node %q output_map: %w", spec.ID, err)
			}
			mappedNode.InvokeSubgraph = runner
			return mappedNode, nil
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
		ResolveStateContract: resolveHumanMessageStateContract,
		Build: func(ctx *registry.BuildContext, spec dsl.GraphNodeSpec) (node.Node, error) {
			_ = ctx
			humanNode := node.NewHumanMessageNode(config.String(spec.Config, "content"), node.WithScope(nodeStateScope(spec.Config)), node.WithID(spec.ID))
			applyNodeMetadata(&humanNode.Base, spec)
			if value := config.String(spec.Config, "interrupt_message"); value != "" {
				humanNode.InterruptMessage = value
			}
			return humanNode, nil
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
		ResolveStateContract: resolveContextReducerStateContract,
		Build: func(ctx *registry.BuildContext, spec dsl.GraphNodeSpec) (node.Node, error) {
			_ = ctx
			reducerNode := node.NewContextReducerNode(node.WithScope(nodeStateScope(spec.Config)), node.WithID(spec.ID))
			applyNodeMetadata(&reducerNode.Base, spec)
			reducerNode.MaxMessages, _ = config.Int(spec.Config, "max_messages")
			if value, ok := config.Bool(spec.Config, "preserve_system"); ok {
				reducerNode.PreserveSystem = value
			}
			reducerNode.PreserveRecent, _ = config.Int(spec.Config, "preserve_recent")
			if value := config.String(spec.Config, "summary_prefix"); value != "" {
				reducerNode.SummaryPrefix = value
			}
			return reducerNode, nil
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
		ResolveStateContract: resolveLLMStateContract,
		Build: func(ctx *registry.BuildContext, spec dsl.GraphNodeSpec) (node.Node, error) {
			_ = ctx
			llmNode := node.NewLLMNode(node.WithScope(nodeStateScope(spec.Config)), node.WithID(spec.ID))
			applyNodeMetadata(&llmNode.Base, spec)
			llmNode.ToolIDs = config.StringSlice(spec.Config, "tool_ids")
			llmNode.PromptMaxChars, _ = config.Int(spec.Config, "prompt_max_chars")
			return llmNode, nil
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
		ResolveStateContract: resolveToolsStateContract,
		Build: func(ctx *registry.BuildContext, spec dsl.GraphNodeSpec) (node.Node, error) {
			_ = ctx
			toolsNode := node.NewToolsNode(node.WithScope(nodeStateScope(spec.Config)), node.WithID(spec.ID))
			applyNodeMetadata(&toolsNode.Base, spec)
			toolsNode.ToolIDs = config.StringSlice(spec.Config, "tool_ids")
			if parallel, ok := config.Bool(spec.Config, "parallel"); ok {
				toolsNode.Parallel = parallel
			}
			return toolsNode, nil
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
		ResolveStateContract: resolveAgentStateContract,
		Build: func(ctx *registry.BuildContext, spec dsl.GraphNodeSpec) (node.Node, error) {
			_ = ctx
			agentNode := node.NewAgentNode(node.WithScope(nodeStateScope(spec.Config)), node.WithID(spec.ID))
			applyNodeMetadata(&agentNode.Base, spec)
			agentNode.ToolIDs = config.StringSlice(spec.Config, "tool_ids")
			agentNode.SystemPrompt = config.String(spec.Config, "system_prompt")
			var err error
			agentNode.InputPath, err = parseOptionalStatePath(config.String(spec.Config, "input_path"))
			if err != nil {
				return nil, fmt.Errorf("build agent node %q input_path: %w", spec.ID, err)
			}
			agentNode.OutputPath, err = parseOptionalStatePath(config.String(spec.Config, "output_path"))
			if err != nil {
				return nil, fmt.Errorf("build agent node %q output_path: %w", spec.ID, err)
			}
			agentNode.MaxIterations, _ = config.Int(spec.Config, "max_iterations")
			agentNode.PromptMaxChars, _ = config.Int(spec.Config, "prompt_max_chars")
			if parallel, ok := config.Bool(spec.Config, "parallel"); ok {
				agentNode.Parallel = parallel
			}
			agentNode.ToolName = config.String(spec.Config, "tool_name")
			agentNode.ToolDescription = config.String(spec.Config, "tool_description")
			return agentNode, nil
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
			cfg, err := ParseExpressionConditionConfig(spec.Config)
			if err != nil {
				return registry.EdgeCondition{}, fmt.Errorf("resolve expression condition: %w", err)
			}
			return ExpressionConditions(cfg)
		},
	})
}

func conditionStateScope(configMap map[string]any) string {
	if _, ok := configMap["state_scope"]; ok {
		return config.String(configMap, "state_scope")
	}
	return node.DefaultScope
}

func nodeStateScope(configMap map[string]any) string {
	if _, ok := configMap["state_scope"]; ok {
		return config.String(configMap, "state_scope")
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
