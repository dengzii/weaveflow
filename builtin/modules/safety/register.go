package safety

import (
	"context"
	"fmt"
	"strings"

	"weaveflow/core"
	"weaveflow/dsl"
	"weaveflow/nodes"
	"weaveflow/registry"
	wfstate "weaveflow/state"
)

type legacyNodeBuilder func(*registry.BuildContext, dsl.GraphNodeSpec) (core.Node[wfstate.State], error)

func adaptLegacyNodeBuilder(build legacyNodeBuilder) func(registry.NodeBuildContext, dsl.GraphNodeSpec) (core.Node[wfstate.State], error) {
	return func(ctx registry.NodeBuildContext, spec dsl.GraphNodeSpec) (core.Node[wfstate.State], error) {
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

func toolPolicyCheckStateFieldDefinition() dsl.StateFieldDefinition {
	return dsl.StateFieldDefinition{
		Name:        wfstate.StateKeyToolPolicyCheck,
		Description: "Tool policy check results: per-call decisions, blocked and approved call lists.",
		Schema: dsl.JSONSchema{
			"type": "object",
			"properties": dsl.JSONSchema{
				"action": dsl.JSONSchema{
					"type": "string",
					"enum": []string{"allow", "deny", "needs_approval"},
				},
				"decisions": dsl.JSONSchema{
					"type": "array",
					"items": dsl.JSONSchema{
						"type": "object",
						"properties": dsl.JSONSchema{
							"tool_call_id": dsl.JSONSchema{"type": "string"},
							"tool_name":    dsl.JSONSchema{"type": "string"},
							"action":       dsl.JSONSchema{"type": "string"},
							"reason":       dsl.JSONSchema{"type": "string"},
						},
					},
				},
				"blocked_calls": dsl.JSONSchema{
					"type":  "array",
					"items": dsl.JSONSchema{"type": "object"},
				},
				"approved_calls": dsl.JSONSchema{
					"type":  "array",
					"items": dsl.JSONSchema{"type": "object"},
				},
				"checked_at": dsl.JSONSchema{"type": "string"},
			},
			"additionalProperties": true,
		},
	}
}

func approvalStateFieldDefinition() dsl.StateFieldDefinition {
	return dsl.StateFieldDefinition{
		Name:        wfstate.StateKeyApproval,
		Description: "Human approval state for high-risk tool calls.",
		Schema: dsl.JSONSchema{
			"type": "object",
			"properties": dsl.JSONSchema{
				"status": dsl.JSONSchema{
					"type": "string",
					"enum": []string{"pending", "approved", "rejected", "partial"},
				},
				"decisions":  dsl.JSONSchema{"type": "array"},
				"decided_at": dsl.JSONSchema{"type": "string"},
			},
			"additionalProperties": true,
		},
	}
}

func budgetStateFieldDefinition() dsl.StateFieldDefinition {
	return dsl.StateFieldDefinition{
		Name:        wfstate.StateKeyBudget,
		Description: "Resource budget tracking: token usage, tool calls, and iterations against limits.",
		Schema: dsl.JSONSchema{
			"type": "object",
			"properties": dsl.JSONSchema{
				"usage": dsl.JSONSchema{
					"type": "object",
					"properties": dsl.JSONSchema{
						"total_tokens": dsl.JSONSchema{"type": "integer"},
						"llm_calls":    dsl.JSONSchema{"type": "integer"},
						"tool_calls":   dsl.JSONSchema{"type": "integer"},
						"iterations":   dsl.JSONSchema{"type": "integer"},
					},
				},
				"limits": dsl.JSONSchema{
					"type": "object",
					"properties": dsl.JSONSchema{
						"max_tokens":     dsl.JSONSchema{"type": "integer"},
						"max_tool_calls": dsl.JSONSchema{"type": "integer"},
						"max_iterations": dsl.JSONSchema{"type": "integer"},
					},
				},
				"status": dsl.JSONSchema{
					"type": "string",
					"enum": []string{"ok", "warning", "exceeded"},
				},
				"exceeded_limits": dsl.JSONSchema{
					"type":  "array",
					"items": dsl.JSONSchema{"type": "string"},
				},
				"checked_at": dsl.JSONSchema{"type": "string"},
			},
			"additionalProperties": true,
		},
	}
}

func toolPolicyGuardNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "tool_policy_guard",
			Title:       "Tool Policy Guard",
			Description: "Check tool calls against safety policies before execution.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"state_scope": dsl.JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
		Build: adaptLegacyNodeBuilder(func(ctx *registry.BuildContext, spec dsl.GraphNodeSpec) (core.Node[wfstate.State], error) {
			node := nodes.NewToolPolicyGuardNode()
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			node.StateScope = registry.StringConfigTrim(spec.Config, "state_scope")
			return node, nil
		}),
		ResolveStateContract: resolveToolPolicyGuardStateContract,
	}
}

func approvalGateNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "approval_gate",
			Title:       "Approval Gate",
			Description: "Pause execution for human approval of high-risk tool calls.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"state_scope":       dsl.JSONSchema{"type": "string"},
					"interrupt_message": dsl.JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
		Build: adaptLegacyNodeBuilder(func(ctx *registry.BuildContext, spec dsl.GraphNodeSpec) (core.Node[wfstate.State], error) {
			node := nodes.NewApprovalGateNode()
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			node.StateScope = registry.StringConfigTrim(spec.Config, "state_scope")
			if message := registry.StringConfigTrim(spec.Config, "interrupt_message"); message != "" {
				node.InterruptMessage = message
			}
			return node, nil
		}),
		ResolveStateContract: resolveApprovalGateStateContract,
	}
}

func costBudgetGuardNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "cost_budget_guard",
			Title:       "Cost Budget Guard",
			Description: "Track token usage, tool calls, and iterations against configurable budget limits.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"state_scope":       dsl.JSONSchema{"type": "string"},
					"max_tokens":        dsl.JSONSchema{"type": "integer", "minimum": 1},
					"max_tool_calls":    dsl.JSONSchema{"type": "integer", "minimum": 1},
					"max_iterations":    dsl.JSONSchema{"type": "integer", "minimum": 1},
					"warning_threshold": dsl.JSONSchema{"type": "number", "minimum": 0, "maximum": 1},
				},
				"additionalProperties": false,
			},
		},
		Build: adaptLegacyNodeBuilder(func(ctx *registry.BuildContext, spec dsl.GraphNodeSpec) (core.Node[wfstate.State], error) {
			node := nodes.NewCostBudgetGuardNode()
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			node.StateScope = registry.StringConfigTrim(spec.Config, "state_scope")
			if value, ok := registry.IntConfig(spec.Config, "max_tokens"); ok {
				node.MaxTokens = value
			}
			if value, ok := registry.IntConfig(spec.Config, "max_tool_calls"); ok {
				node.MaxToolCalls = value
			}
			if value, ok := registry.IntConfig(spec.Config, "max_iterations"); ok {
				node.MaxIterations = value
			}
			if value, ok := registry.FloatConfig(spec.Config, "warning_threshold"); ok {
				node.WarningThreshold = value
			}
			return node, nil
		}),
		ResolveStateContract: resolveCostBudgetGuardStateContract,
	}
}

func toolPolicyCheckActionConditionDefinition() registry.ConditionDefinition {
	return registry.ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type:        "tool_policy_check_action",
			Title:       "Tool Policy Check Action",
			Description: "Routes based on tool_policy_check.action value (allow, deny, needs_approval).",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"action": dsl.JSONSchema{
						"type": "string",
						"enum": []string{"allow", "deny", "needs_approval"},
					},
				},
				"required":             []string{"action"},
				"additionalProperties": false,
			},
		},
		Resolve: func(spec dsl.GraphConditionSpec) (registry.EdgeCondition, error) {
			expected := strings.ToLower(strings.TrimSpace(registry.StringConfigTrim(spec.Config, "action")))
			if expected == "" {
				return registry.EdgeCondition{}, fmt.Errorf("tool_policy_check_action: action config is required")
			}
			return registry.NewEdgeCondition(dsl.GraphConditionSpec{
				Type:   "tool_policy_check_action",
				Config: map[string]any{"action": expected},
			}, func(_ context.Context, state wfstate.State) bool {
				check := state.Get(wfstate.StateKeyToolPolicyCheck)
				if check == nil {
					return false
				}
				actual, _ := check["action"].(string)
				return strings.EqualFold(actual, expected)
			}), nil
		},
	}
}

func approvalStatusEqualsConditionDefinition() registry.ConditionDefinition {
	return registry.ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type:        "approval_status_equals",
			Title:       "Approval Status Equals",
			Description: "Routes based on approval.status value (pending, approved, rejected, partial).",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"status": dsl.JSONSchema{
						"type": "string",
						"enum": []string{"pending", "approved", "rejected", "partial"},
					},
				},
				"required":             []string{"status"},
				"additionalProperties": false,
			},
		},
		Resolve: func(spec dsl.GraphConditionSpec) (registry.EdgeCondition, error) {
			expected := strings.ToLower(strings.TrimSpace(registry.StringConfigTrim(spec.Config, "status")))
			if expected == "" {
				return registry.EdgeCondition{}, fmt.Errorf("approval_status_equals: status config is required")
			}
			return registry.NewEdgeCondition(dsl.GraphConditionSpec{
				Type:   "approval_status_equals",
				Config: map[string]any{"status": expected},
			}, func(_ context.Context, state wfstate.State) bool {
				approval := state.Get(wfstate.StateKeyApproval)
				if approval == nil {
					return false
				}
				actual, _ := approval["status"].(string)
				return strings.EqualFold(actual, expected)
			}), nil
		},
	}
}

func budgetStatusEqualsConditionDefinition() registry.ConditionDefinition {
	return registry.ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type:        "budget_status_equals",
			Title:       "Budget Status Equals",
			Description: "Routes based on budget.status value (ok, warning, exceeded).",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"status": dsl.JSONSchema{
						"type": "string",
						"enum": []string{"ok", "warning", "exceeded"},
					},
				},
				"required":             []string{"status"},
				"additionalProperties": false,
			},
		},
		Resolve: func(spec dsl.GraphConditionSpec) (registry.EdgeCondition, error) {
			expected := strings.ToLower(strings.TrimSpace(registry.StringConfigTrim(spec.Config, "status")))
			if expected == "" {
				return registry.EdgeCondition{}, fmt.Errorf("budget_status_equals: status config is required")
			}
			return registry.NewEdgeCondition(dsl.GraphConditionSpec{
				Type:   "budget_status_equals",
				Config: map[string]any{"status": expected},
			}, func(_ context.Context, state wfstate.State) bool {
				budget := state.Get(wfstate.StateKeyBudget)
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
	scope := strings.TrimSpace(registry.StringConfigTrim(spec.Config, "state_scope"))

	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{
				Path:        canonicalContractPath(wfstate.StateKeyToolPolicy),
				Mode:        dsl.StateAccessRead,
				Description: "Tool safety policy rules.",
			},
			{
				Path:        scopedConversationPath(scope, "messages"),
				Mode:        dsl.StateAccessRead,
				Description: "Conversation messages with tool calls to check.",
			},
			{
				Path:          canonicalContractPath(wfstate.StateKeyToolPolicyCheck),
				Mode:          dsl.StateAccessWrite,
				Required:      true,
				Description:   "Policy check results.",
				MergeStrategy: dsl.StateMergeMerge,
			},
		},
	}, nil
}

func resolveApprovalGateStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	scope := strings.TrimSpace(registry.StringConfigTrim(spec.Config, "state_scope"))

	fields := []dsl.StateFieldRef{
		{
			Path:        canonicalContractPath(wfstate.StateKeyToolPolicyCheck),
			Mode:        dsl.StateAccessReadWrite,
			Description: "Policy check decisions to review and update after approval.",
		},
		{
			Path:          canonicalContractPath(wfstate.StateKeyApproval),
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
	scope := strings.TrimSpace(registry.StringConfigTrim(spec.Config, "state_scope"))

	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{
				Path:        canonicalContractPath(nodes.TokenUsageStateKey),
				Mode:        dsl.StateAccessRead,
				Description: "Token usage metrics.",
			},
			{
				Path:        canonicalContractPath(wfstate.StateKeyObservations),
				Mode:        dsl.StateAccessRead,
				Description: "Observations for tool call counting.",
			},
			{
				Path:        scopedConversationPath(scope, "iteration_count"),
				Mode:        dsl.StateAccessRead,
				Description: "Conversation iteration count.",
			},
			{
				Path:          canonicalContractPath(wfstate.StateKeyBudget),
				Mode:          dsl.StateAccessWrite,
				Required:      true,
				Description:   "Budget status and usage.",
				MergeStrategy: dsl.StateMergeMerge,
			},
		},
	}, nil
}

func canonicalContractPath(path string) string {
	return wfstate.NormalizeContractPath(path)
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

func scopedStatePath(scope string, field string) string {
	scope = strings.TrimSpace(scope)
	field = strings.TrimSpace(field)
	if scope == "" {
		return canonicalContractPath(field)
	}
	if field == "" {
		return "scopes." + scope
	}
	return "scopes." + scope + "." + field
}
