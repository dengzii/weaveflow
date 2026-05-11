package verification

import (
	"context"
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
	registry.RegisterStateField(verificationStateFieldDefinition())
	registry.RegisterStateField(finalStateFieldDefinition())
	registry.RegisterNodeType(verifierNodeTypeDefinition())
	registry.RegisterNodeType(finalizerNodeTypeDefinition())
	registry.RegisterCondition(verificationNextActionConditionDefinition())
}

func verificationStateFieldDefinition() dsl.StateFieldDefinition {
	return dsl.StateFieldDefinition{
		Name:        fruntime.StateKeyVerification,
		Description: "Verification results for step-level and final-level acceptance checks.",
		Schema: dsl.JSONSchema{
			"type": "object",
			"properties": dsl.JSONSchema{
				"status": dsl.JSONSchema{
					"type": "string",
					"enum": []string{"pass", "fail", "partial", "inconclusive"},
				},
				"issues":       dsl.JSONSchema{"type": "array", "items": dsl.JSONSchema{"type": "string"}},
				"summary":      dsl.JSONSchema{"type": "string"},
				"next_action":  dsl.JSONSchema{"type": "string"},
				"needs_replan": dsl.JSONSchema{"type": "boolean"},
				"needs_retry":  dsl.JSONSchema{"type": "boolean"},
			},
			"additionalProperties": true,
		},
	}
}

func finalStateFieldDefinition() dsl.StateFieldDefinition {
	return dsl.StateFieldDefinition{
		Name:        fruntime.StateKeyFinal,
		Description: "Final answer state produced by the finalizer node.",
		Schema: dsl.JSONSchema{
			"type": "object",
			"properties": dsl.JSONSchema{
				"answer": dsl.JSONSchema{"type": "string"},
				"status": dsl.JSONSchema{
					"type": "string",
					"enum": []string{"success", "failed", "blocked", "needs_clarification"},
				},
				"evidence": dsl.JSONSchema{
					"type":  "array",
					"items": dsl.JSONSchema{"type": "string"},
				},
			},
			"additionalProperties": true,
		},
	}
}

func verifierNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "verifier",
			Title:       "Verifier Node",
			Description: "Verify step or final results against acceptance criteria using LLM-based or rule-based checks.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"state_scope":        dsl.JSONSchema{"type": "string"},
					"mode":               dsl.JSONSchema{"type": "string", "enum": []string{"step", "final", "auto"}},
					"planner_state_path": dsl.JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
		Build: adaptLegacyNodeBuilder(func(ctx *registry.BuildContext, spec dsl.GraphNodeSpec) (core.Node[fruntime.State], error) {
			node := nodes.NewVerifierNode()
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			node.StateScope = stringConfig(spec.Config, "state_scope")
			node.Mode = stringConfig(spec.Config, "mode")
			node.PlannerStatePath = stringConfig(spec.Config, "planner_state_path")
			return node, nil
		}),
		ResolveStateContract: resolveVerifierStateContract,
	}
}

func finalizerNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "finalizer",
			Title:       "Finalizer Node",
			Description: "Generate the final answer from verification results, observations, and evidence.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"state_scope":        dsl.JSONSchema{"type": "string"},
					"planner_state_path": dsl.JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
		Build: adaptLegacyNodeBuilder(func(ctx *registry.BuildContext, spec dsl.GraphNodeSpec) (core.Node[fruntime.State], error) {
			node := nodes.NewFinalizerNode()
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			node.StateScope = stringConfig(spec.Config, "state_scope")
			node.PlannerStatePath = stringConfig(spec.Config, "planner_state_path")
			return node, nil
		}),
		ResolveStateContract: resolveFinalizerStateContract,
	}
}

func verificationNextActionConditionDefinition() registry.ConditionDefinition {
	return registry.ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type:        "verification_next_action_equals",
			Title:       "Verification Next Action Equals",
			Description: "Routes based on verification.next_action value (continue, retry, replan, finalize, clarify).",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"action": dsl.JSONSchema{
						"type": "string",
						"enum": []string{"continue", "retry", "replan", "finalize", "clarify"},
					},
				},
				"required":             []string{"action"},
				"additionalProperties": false,
			},
		},
		Resolve: func(spec dsl.GraphConditionSpec) (registry.EdgeCondition, error) {
			expected := strings.ToLower(strings.TrimSpace(stringConfig(spec.Config, "action")))
			if expected == "" {
				return registry.EdgeCondition{}, fmt.Errorf("verification_next_action_equals: action config is required")
			}
			return registry.NewEdgeCondition(dsl.GraphConditionSpec{
				Type: "verification_next_action_equals",
				Config: map[string]any{
					"action": expected,
				},
			}, func(_ context.Context, state fruntime.State) bool {
				v := state.Get(fruntime.StateKeyVerification)
				if v == nil {
					return false
				}
				actual, _ := v["next_action"].(string)
				return strings.EqualFold(actual, expected)
			}), nil
		},
	}
}

func resolveVerifierStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	scope := strings.TrimSpace(stringConfig(spec.Config, "state_scope"))
	if scope == "" {
		scope = "default"
	}
	plannerPath := strings.TrimSpace(stringConfig(spec.Config, "planner_state_path"))
	if plannerPath == "" {
		plannerPath = fruntime.StateKeyPlanner
	}
	plannerPath = canonicalContractPath(plannerPath)

	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{
				Path:        plannerPath + ".plan",
				Mode:        dsl.StateAccessReadWrite,
				Description: "Plan steps to verify and update status.",
			},
			{
				Path:        plannerPath + ".current_step_id",
				Mode:        dsl.StateAccessRead,
				Description: "Current step being verified.",
			},
			{
				Path:        plannerPath + ".objective",
				Mode:        dsl.StateAccessRead,
				Description: "Task objective for final verification.",
			},
			{
				Path:        canonicalContractPath(fruntime.StateKeyExecution + ".route"),
				Mode:        dsl.StateAccessRead,
				Description: "Execution route used to auto-select step or final verification mode.",
			},
			{
				Path:        canonicalContractPath(fruntime.StateKeyExecution + ".step_results"),
				Mode:        dsl.StateAccessRead,
				Description: "Step execution results.",
			},
			{
				Path:        canonicalContractPath(fruntime.StateKeyRequest + ".input"),
				Mode:        dsl.StateAccessRead,
				Description: "Request input fallback used when planner objective is unavailable.",
			},
			{
				Path:        canonicalContractPath(fruntime.StateKeyObservations),
				Mode:        dsl.StateAccessRead,
				Description: "Observations for verification.",
			},
			{
				Path:        canonicalContractPath(fruntime.StateKeyEvidence),
				Mode:        dsl.StateAccessRead,
				Description: "Evidence for verification.",
			},
			{
				Path:        scopedStatePath(scope, "messages"),
				Mode:        dsl.StateAccessRead,
				Description: "Conversation messages.",
			},
			{
				Path:        scopedStatePath(scope, "final_answer"),
				Mode:        dsl.StateAccessRead,
				Description: "Current scoped final answer used during final verification.",
			},
			{
				Path:          canonicalContractPath(fruntime.StateKeyVerification),
				Mode:          dsl.StateAccessWrite,
				Required:      true,
				Description:   "Verification result output.",
				MergeStrategy: dsl.StateMergeMerge,
			},
			{
				Path:          canonicalContractPath(nodes.TokenUsageStateKey),
				Mode:          dsl.StateAccessWrite,
				Description:   "Accumulated token usage emitted by verifier LLM calls.",
				MergeStrategy: dsl.StateMergeMerge,
			},
		},
	}, nil
}

func resolveFinalizerStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	scope := strings.TrimSpace(stringConfig(spec.Config, "state_scope"))
	if scope == "" {
		scope = "default"
	}
	plannerPath := strings.TrimSpace(stringConfig(spec.Config, "planner_state_path"))
	if plannerPath == "" {
		plannerPath = fruntime.StateKeyPlanner
	}
	plannerPath = canonicalContractPath(plannerPath)

	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{
				Path:        canonicalContractPath(fruntime.StateKeyVerification),
				Mode:        dsl.StateAccessRead,
				Description: "Verification results.",
			},
			{
				Path:        canonicalContractPath(fruntime.StateKeyObservations),
				Mode:        dsl.StateAccessRead,
				Description: "All observations.",
			},
			{
				Path:        canonicalContractPath(fruntime.StateKeyEvidence),
				Mode:        dsl.StateAccessRead,
				Description: "All evidence.",
			},
			{
				Path:        plannerPath + ".summary",
				Mode:        dsl.StateAccessRead,
				Description: "Plan summary.",
			},
			{
				Path:        plannerPath + ".objective",
				Mode:        dsl.StateAccessRead,
				Description: "Task objective.",
			},
			{
				Path:        scopedStatePath(scope, "messages"),
				Mode:        dsl.StateAccessRead,
				Description: "Conversation messages.",
			},
			{
				Path:          scopedStatePath(scope, "final_answer"),
				Mode:          dsl.StateAccessWrite,
				Description:   "Final answer in conversation scope.",
				MergeStrategy: dsl.StateMergeReplace,
			},
			{
				Path:          canonicalContractPath(fruntime.StateKeyFinal),
				Mode:          dsl.StateAccessWrite,
				Required:      true,
				Description:   "Final answer state subtree.",
				MergeStrategy: dsl.StateMergeMerge,
			},
		},
	}, nil
}

func stringConfig(config map[string]any, key string) string {
	if len(config) == 0 {
		return ""
	}
	value, _ := config[key].(string)
	return strings.TrimSpace(value)
}

func canonicalContractPath(path string) string {
	return fruntime.NormalizeContractPath(path)
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
