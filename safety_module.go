package weaveflow

import (
	"context"
	"fmt"
	"strings"
	"weaveflow/dsl"
	"weaveflow/nodes"
	fruntime "weaveflow/runtime"
)

func RegisterSafetyModule(registry *Registry) {
	if registry == nil {
		return
	}

	registry.RegisterStateField(toolPolicyCheckStateFieldDefinition())
	registry.RegisterStateField(approvalStateFieldDefinition())
	registry.RegisterStateField(budgetStateFieldDefinition())

	registry.RegisterNodeType(toolPolicyGuardNodeTypeDefinition())
	registry.RegisterNodeType(approvalGateNodeTypeDefinition())
	registry.RegisterNodeType(costBudgetGuardNodeTypeDefinition())

	registry.RegisterCondition(toolPolicyCheckActionConditionDefinition())
	registry.RegisterCondition(approvalStatusEqualsConditionDefinition())
	registry.RegisterCondition(budgetStatusEqualsConditionDefinition())
}

func toolPolicyCheckStateFieldDefinition() StateFieldDefinition {
	return StateFieldDefinition{
		Name:        fruntime.StateKeyToolPolicyCheck,
		Description: "Tool policy check results: per-call decisions, blocked and approved call lists.",
		Schema: JSONSchema{
			"type": "object",
			"properties": JSONSchema{
				"action": JSONSchema{
					"type": "string",
					"enum": []string{"allow", "deny", "needs_approval"},
				},
				"decisions": JSONSchema{
					"type": "array",
					"items": JSONSchema{
						"type": "object",
						"properties": JSONSchema{
							"tool_call_id": JSONSchema{"type": "string"},
							"tool_name":    JSONSchema{"type": "string"},
							"action":       JSONSchema{"type": "string"},
							"reason":       JSONSchema{"type": "string"},
						},
					},
				},
				"blocked_calls": JSONSchema{
					"type":  "array",
					"items": JSONSchema{"type": "object"},
				},
				"approved_calls": JSONSchema{
					"type":  "array",
					"items": JSONSchema{"type": "object"},
				},
				"checked_at": JSONSchema{"type": "string"},
			},
			"additionalProperties": true,
		},
	}
}

func approvalStateFieldDefinition() StateFieldDefinition {
	return StateFieldDefinition{
		Name:        fruntime.StateKeyApproval,
		Description: "Human approval state for high-risk tool calls.",
		Schema: JSONSchema{
			"type": "object",
			"properties": JSONSchema{
				"status": JSONSchema{
					"type": "string",
					"enum": []string{"pending", "approved", "rejected", "partial"},
				},
				"decisions":  JSONSchema{"type": "array"},
				"decided_at": JSONSchema{"type": "string"},
			},
			"additionalProperties": true,
		},
	}
}

func budgetStateFieldDefinition() StateFieldDefinition {
	return StateFieldDefinition{
		Name:        fruntime.StateKeyBudget,
		Description: "Resource budget tracking: token usage, tool calls, and iterations against limits.",
		Schema: JSONSchema{
			"type": "object",
			"properties": JSONSchema{
				"usage": JSONSchema{
					"type": "object",
					"properties": JSONSchema{
						"total_tokens": JSONSchema{"type": "integer"},
						"llm_calls":    JSONSchema{"type": "integer"},
						"tool_calls":   JSONSchema{"type": "integer"},
						"iterations":   JSONSchema{"type": "integer"},
					},
				},
				"limits": JSONSchema{
					"type": "object",
					"properties": JSONSchema{
						"max_tokens":     JSONSchema{"type": "integer"},
						"max_tool_calls": JSONSchema{"type": "integer"},
						"max_iterations": JSONSchema{"type": "integer"},
					},
				},
				"status": JSONSchema{
					"type": "string",
					"enum": []string{"ok", "warning", "exceeded"},
				},
				"exceeded_limits": JSONSchema{
					"type":  "array",
					"items": JSONSchema{"type": "string"},
				},
				"checked_at": JSONSchema{"type": "string"},
			},
			"additionalProperties": true,
		},
	}
}

func toolPolicyGuardNodeTypeDefinition() NodeTypeDefinition {
	return NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "tool_policy_guard",
			Title:       "Tool Policy Guard",
			Description: "Check tool calls against safety policies before execution.",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"state_scope": JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
		Build: func(ctx *BuildContext, spec dsl.GraphNodeSpec) (nodes.Node[State], error) {
			node := nodes.NewToolPolicyGuardNode()
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			node.StateScope = stringConfig(spec.Config, "state_scope")
			return node, nil
		},
		ResolveStateContract: resolveToolPolicyGuardStateContract,
	}
}

func approvalGateNodeTypeDefinition() NodeTypeDefinition {
	return NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "approval_gate",
			Title:       "Approval Gate",
			Description: "Pause execution for human approval of high-risk tool calls.",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"state_scope":       JSONSchema{"type": "string"},
					"interrupt_message": JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
		Build: func(ctx *BuildContext, spec dsl.GraphNodeSpec) (nodes.Node[State], error) {
			node := nodes.NewApprovalGateNode()
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			node.StateScope = stringConfig(spec.Config, "state_scope")
			if message := stringConfig(spec.Config, "interrupt_message"); message != "" {
				node.InterruptMessage = message
			}
			return node, nil
		},
		ResolveStateContract: resolveApprovalGateStateContract,
	}
}

func costBudgetGuardNodeTypeDefinition() NodeTypeDefinition {
	return NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "cost_budget_guard",
			Title:       "Cost Budget Guard",
			Description: "Track token usage, tool calls, and iterations against configurable budget limits.",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"state_scope":       JSONSchema{"type": "string"},
					"max_tokens":        JSONSchema{"type": "integer", "minimum": 1},
					"max_tool_calls":    JSONSchema{"type": "integer", "minimum": 1},
					"max_iterations":    JSONSchema{"type": "integer", "minimum": 1},
					"warning_threshold": JSONSchema{"type": "number", "minimum": 0, "maximum": 1},
				},
				"additionalProperties": false,
			},
		},
		Build: func(ctx *BuildContext, spec dsl.GraphNodeSpec) (nodes.Node[State], error) {
			node := nodes.NewCostBudgetGuardNode()
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			node.StateScope = stringConfig(spec.Config, "state_scope")
			if v, ok := intConfig(spec.Config, "max_tokens"); ok {
				node.MaxTokens = v
			}
			if v, ok := intConfig(spec.Config, "max_tool_calls"); ok {
				node.MaxToolCalls = v
			}
			if v, ok := intConfig(spec.Config, "max_iterations"); ok {
				node.MaxIterations = v
			}
			if v, ok := floatConfig(spec.Config, "warning_threshold"); ok {
				node.WarningThreshold = v
			}
			return node, nil
		},
		ResolveStateContract: resolveCostBudgetGuardStateContract,
	}
}

func toolPolicyCheckActionConditionDefinition() ConditionDefinition {
	return ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type:        "tool_policy_check_action",
			Title:       "Tool Policy Check Action",
			Description: "Routes based on tool_policy_check.action value (allow, deny, needs_approval).",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"action": JSONSchema{
						"type": "string",
						"enum": []string{"allow", "deny", "needs_approval"},
					},
				},
				"required":             []string{"action"},
				"additionalProperties": false,
			},
		},
		Resolve: func(spec GraphConditionSpec) (EdgeCondition, error) {
			expected := strings.ToLower(strings.TrimSpace(stringConfig(spec.Config, "action")))
			if expected == "" {
				return EdgeCondition{}, fmt.Errorf("tool_policy_check_action: action config is required")
			}
			return NewEdgeCondition(GraphConditionSpec{
				Type:   "tool_policy_check_action",
				Config: map[string]any{"action": expected},
			}, func(_ context.Context, state State) bool {
				check := state.Get(fruntime.StateKeyToolPolicyCheck)
				if check == nil {
					return false
				}
				actual, _ := check["action"].(string)
				return strings.EqualFold(actual, expected)
			}), nil
		},
	}
}

func approvalStatusEqualsConditionDefinition() ConditionDefinition {
	return ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type:        "approval_status_equals",
			Title:       "Approval Status Equals",
			Description: "Routes based on approval.status value (pending, approved, rejected, partial).",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"status": JSONSchema{
						"type": "string",
						"enum": []string{"pending", "approved", "rejected", "partial"},
					},
				},
				"required":             []string{"status"},
				"additionalProperties": false,
			},
		},
		Resolve: func(spec GraphConditionSpec) (EdgeCondition, error) {
			expected := strings.ToLower(strings.TrimSpace(stringConfig(spec.Config, "status")))
			if expected == "" {
				return EdgeCondition{}, fmt.Errorf("approval_status_equals: status config is required")
			}
			return NewEdgeCondition(GraphConditionSpec{
				Type:   "approval_status_equals",
				Config: map[string]any{"status": expected},
			}, func(_ context.Context, state State) bool {
				approval := state.Get(fruntime.StateKeyApproval)
				if approval == nil {
					return false
				}
				actual, _ := approval["status"].(string)
				return strings.EqualFold(actual, expected)
			}), nil
		},
	}
}

func budgetStatusEqualsConditionDefinition() ConditionDefinition {
	return ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type:        "budget_status_equals",
			Title:       "Budget Status Equals",
			Description: "Routes based on budget.status value (ok, warning, exceeded).",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"status": JSONSchema{
						"type": "string",
						"enum": []string{"ok", "warning", "exceeded"},
					},
				},
				"required":             []string{"status"},
				"additionalProperties": false,
			},
		},
		Resolve: func(spec GraphConditionSpec) (EdgeCondition, error) {
			expected := strings.ToLower(strings.TrimSpace(stringConfig(spec.Config, "status")))
			if expected == "" {
				return EdgeCondition{}, fmt.Errorf("budget_status_equals: status config is required")
			}
			return NewEdgeCondition(GraphConditionSpec{
				Type:   "budget_status_equals",
				Config: map[string]any{"status": expected},
			}, func(_ context.Context, state State) bool {
				budget := state.Get(fruntime.StateKeyBudget)
				if budget == nil {
					return false
				}
				actual, _ := budget["status"].(string)
				return strings.EqualFold(actual, expected)
			}), nil
		},
	}
}

func resolveToolPolicyGuardStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	scope := strings.TrimSpace(stringConfig(spec.Config, "state_scope"))

	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{
				Path:        fruntime.StateKeyToolPolicy,
				Mode:        dsl.StateAccessRead,
				Description: "Tool safety policy rules.",
			},
			{
				Path:        scopedConversationPath(scope, "messages"),
				Mode:        dsl.StateAccessRead,
				Description: "Conversation messages with tool calls to check.",
			},
			{
				Path:          fruntime.StateKeyToolPolicyCheck,
				Mode:          dsl.StateAccessWrite,
				Required:      true,
				Description:   "Policy check results.",
				MergeStrategy: dsl.StateMergeMerge,
			},
		},
	}, nil
}

func resolveApprovalGateStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	scope := strings.TrimSpace(stringConfig(spec.Config, "state_scope"))

	fields := []dsl.StateFieldRef{
		{
			Path:        fruntime.StateKeyToolPolicyCheck,
			Mode:        dsl.StateAccessReadWrite,
			Description: "Policy check decisions to review and update after approval.",
		},
		{
			Path:          fruntime.StateKeyApproval,
			Mode:          dsl.StateAccessWrite,
			Required:      true,
			Description:   "Approval result.",
			MergeStrategy: dsl.StateMergeMerge,
		},
	}

	if scope != "" {
		fields = append(fields, dsl.StateFieldRef{
			Path:        scopedStatePath(scope, nodes.PendingApprovalStateKey),
			Mode:        dsl.StateAccessReadWrite,
			Description: "Pending approval input from human.",
		})
	}

	return dsl.StateContract{Fields: fields}, nil
}

func resolveCostBudgetGuardStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	scope := strings.TrimSpace(stringConfig(spec.Config, "state_scope"))

	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{
				Path:        nodes.TokenUsageStateKey,
				Mode:        dsl.StateAccessRead,
				Description: "Token usage metrics.",
			},
			{
				Path:        fruntime.StateKeyObservations,
				Mode:        dsl.StateAccessRead,
				Description: "Observations for tool call counting.",
			},
			{
				Path:        scopedConversationPath(scope, "iteration_count"),
				Mode:        dsl.StateAccessRead,
				Description: "Conversation iteration count.",
			},
			{
				Path:          fruntime.StateKeyBudget,
				Mode:          dsl.StateAccessWrite,
				Required:      true,
				Description:   "Budget status and usage.",
				MergeStrategy: dsl.StateMergeMerge,
			},
		},
	}, nil
}
