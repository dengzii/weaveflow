package weaveflow

import (
	"context"
	"fmt"
	"strings"

	"weaveflow/dsl"
	"weaveflow/nodes"
	fruntime "weaveflow/runtime"
)

func RegisterVerificationModule(registry *Registry) {
	if registry == nil {
		return
	}

	registry.RegisterStateField(verificationStateFieldDefinition())
	registry.RegisterStateField(finalStateFieldDefinition())

	registry.RegisterNodeType(verifierNodeTypeDefinition())
	registry.RegisterNodeType(finalizerNodeTypeDefinition())

	registry.RegisterCondition(verificationNextActionConditionDefinition())
}

func verificationStateFieldDefinition() StateFieldDefinition {
	return StateFieldDefinition{
		Name:        fruntime.StateKeyVerification,
		Description: "Verification results for step-level and final-level acceptance checks.",
		Schema: JSONSchema{
			"type": "object",
			"properties": JSONSchema{
				"status": JSONSchema{
					"type": "string",
					"enum": []string{"pass", "fail", "partial", "inconclusive"},
				},
				"issues":       JSONSchema{"type": "array", "items": JSONSchema{"type": "string"}},
				"summary":      JSONSchema{"type": "string"},
				"next_action":  JSONSchema{"type": "string"},
				"needs_replan": JSONSchema{"type": "boolean"},
				"needs_retry":  JSONSchema{"type": "boolean"},
			},
			"additionalProperties": true,
		},
	}
}

func finalStateFieldDefinition() StateFieldDefinition {
	return StateFieldDefinition{
		Name:        fruntime.StateKeyFinal,
		Description: "Final answer state produced by the finalizer node.",
		Schema: JSONSchema{
			"type": "object",
			"properties": JSONSchema{
				"answer": JSONSchema{"type": "string"},
				"status": JSONSchema{
					"type": "string",
					"enum": []string{"success", "failed", "blocked", "needs_clarification"},
				},
				"evidence": JSONSchema{
					"type":  "array",
					"items": JSONSchema{"type": "string"},
				},
			},
			"additionalProperties": true,
		},
	}
}

func verifierNodeTypeDefinition() NodeTypeDefinition {
	return NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "verifier",
			Title:       "Verifier Node",
			Description: "Verify step or final results against acceptance criteria using LLM-based or rule-based checks.",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"state_scope":        JSONSchema{"type": "string"},
					"mode":               JSONSchema{"type": "string", "enum": []string{"step", "final", "auto"}},
					"planner_state_path": JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
		Build: func(ctx *BuildContext, spec dsl.GraphNodeSpec) (nodes.Node[State], error) {
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
		},
		ResolveStateContract: resolveVerifierStateContract,
	}
}

func finalizerNodeTypeDefinition() NodeTypeDefinition {
	return NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "finalizer",
			Title:       "Finalizer Node",
			Description: "Generate the final answer from verification results, observations, and evidence.",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"state_scope":        JSONSchema{"type": "string"},
					"planner_state_path": JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
		Build: func(ctx *BuildContext, spec dsl.GraphNodeSpec) (nodes.Node[State], error) {
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
		},
		ResolveStateContract: resolveFinalizerStateContract,
	}
}

func verificationNextActionConditionDefinition() ConditionDefinition {
	return ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type:        "verification_next_action_equals",
			Title:       "Verification Next Action Equals",
			Description: "Routes based on verification.next_action value (continue, retry, replan, finalize, clarify).",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"action": JSONSchema{
						"type": "string",
						"enum": []string{"continue", "retry", "replan", "finalize", "clarify"},
					},
				},
				"required":             []string{"action"},
				"additionalProperties": false,
			},
		},
		Resolve: func(spec GraphConditionSpec) (EdgeCondition, error) {
			expected := strings.ToLower(strings.TrimSpace(stringConfig(spec.Config, "action")))
			if expected == "" {
				return EdgeCondition{}, fmt.Errorf("verification_next_action_equals: action config is required")
			}
			return NewEdgeCondition(GraphConditionSpec{
				Type: "verification_next_action_equals",
				Config: map[string]any{
					"action": expected,
				},
			}, func(_ context.Context, state State) bool {
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
				Path:        fruntime.StateKeyExecution + ".step_results",
				Mode:        dsl.StateAccessRead,
				Description: "Step execution results.",
			},
			{
				Path:        fruntime.StateKeyObservations,
				Mode:        dsl.StateAccessRead,
				Description: "Observations for verification.",
			},
			{
				Path:        fruntime.StateKeyEvidence,
				Mode:        dsl.StateAccessRead,
				Description: "Evidence for verification.",
			},
			{
				Path:        scopedStatePath(scope, "messages"),
				Mode:        dsl.StateAccessRead,
				Description: "Conversation messages.",
			},
			{
				Path:          fruntime.StateKeyVerification,
				Mode:          dsl.StateAccessWrite,
				Required:      true,
				Description:   "Verification result output.",
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

	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{
				Path:        fruntime.StateKeyVerification,
				Mode:        dsl.StateAccessRead,
				Description: "Verification results.",
			},
			{
				Path:        fruntime.StateKeyObservations,
				Mode:        dsl.StateAccessRead,
				Description: "All observations.",
			},
			{
				Path:        fruntime.StateKeyEvidence,
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
				Path:          fruntime.StateKeyFinal,
				Mode:          dsl.StateAccessWrite,
				Required:      true,
				Description:   "Final answer state subtree.",
				MergeStrategy: dsl.StateMergeMerge,
			},
		},
	}, nil
}
