package execution

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
	registry.RegisterStateField(executionStateFieldDefinition())
	registry.RegisterStateField(observationsStateFieldDefinition())
	registry.RegisterStateField(evidenceStateFieldDefinition())
	registry.RegisterNodeType(planStepExecutorNodeTypeDefinition())
	registry.RegisterNodeType(observationRecorderNodeTypeDefinition())
	registry.RegisterCondition(executionRouteEqualsConditionDefinition())
}

func executionStateFieldDefinition() dsl.StateFieldDefinition {
	return dsl.StateFieldDefinition{
		Name:        wfstate.StateKeyExecution,
		Description: "Plan step execution state: current step, route decision, and step results.",
		Schema: dsl.JSONSchema{
			"type": "object",
			"properties": dsl.JSONSchema{
				"current_step": dsl.JSONSchema{"type": "object"},
				"route": dsl.JSONSchema{
					"type": "string",
					"enum": []string{"llm", "llm_with_tools", "verifier", "human", "finalize", "blocked"},
				},
				"step_results": dsl.JSONSchema{"type": "object"},
			},
			"additionalProperties": true,
		},
	}
}

func observationsStateFieldDefinition() dsl.StateFieldDefinition {
	return dsl.StateFieldDefinition{
		Name:        wfstate.StateKeyObservations,
		Description: "Structured observations recorded from tool and LLM outputs.",
		Schema: dsl.JSONSchema{
			"type": "array",
			"items": dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"source":     dsl.JSONSchema{"type": "string"},
					"summary":    dsl.JSONSchema{"type": "string"},
					"error":      dsl.JSONSchema{"type": "object"},
					"confidence": dsl.JSONSchema{"type": "number"},
					"step_id":    dsl.JSONSchema{"type": "string"},
					"timestamp":  dsl.JSONSchema{"type": "string"},
					"raw_ref":    dsl.JSONSchema{"type": "string"},
				},
			},
		},
	}
}

func evidenceStateFieldDefinition() dsl.StateFieldDefinition {
	return dsl.StateFieldDefinition{
		Name:        wfstate.StateKeyEvidence,
		Description: "Evidence entries collected from tool outputs and observations.",
		Schema: dsl.JSONSchema{
			"type": "array",
			"items": dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"type":         dsl.JSONSchema{"type": "string"},
					"content":      dsl.JSONSchema{"type": "string"},
					"source":       dsl.JSONSchema{"type": "string"},
					"artifact_ref": dsl.JSONSchema{"type": "string"},
				},
			},
		},
	}
}

func planStepExecutorNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "plan_step_executor",
			Title:       "Plan Step Executor",
			Description: "Select the next ready step from the plan and route execution based on step kind.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"state_scope":        dsl.JSONSchema{"type": "string"},
					"planner_state_path": dsl.JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
		Build: adaptLegacyNodeBuilder(func(ctx *registry.BuildContext, spec dsl.GraphNodeSpec) (core.Node[wfstate.State], error) {
			node := nodes.NewPlanStepExecutorNode()
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			node.StateScope = registry.StringConfigTrim(spec.Config, "state_scope")
			node.PlannerStatePath = registry.StringConfigTrim(spec.Config, "planner_state_path")
			return node, nil
		}),
		ResolveStateContract: resolvePlanStepExecutorStateContract,
	}
}

func observationRecorderNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "observation_recorder",
			Title:       "Observation Recorder",
			Description: "Record structured observations and evidence from recent tool and LLM outputs.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"state_scope":        dsl.JSONSchema{"type": "string"},
					"planner_state_path": dsl.JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
		Build: adaptLegacyNodeBuilder(func(ctx *registry.BuildContext, spec dsl.GraphNodeSpec) (core.Node[wfstate.State], error) {
			node := nodes.NewObservationRecorderNode()
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			node.StateScope = registry.StringConfigTrim(spec.Config, "state_scope")
			node.PlannerStatePath = registry.StringConfigTrim(spec.Config, "planner_state_path")
			return node, nil
		}),
		ResolveStateContract: resolveObservationRecorderStateContract,
	}
}

func executionRouteEqualsConditionDefinition() registry.ConditionDefinition {
	return registry.ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type:        "execution_route_equals",
			Title:       "Execution Route Equals",
			Description: "Routes based on the execution.route value set by plan_step_executor.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"route": dsl.JSONSchema{
						"type": "string",
						"enum": []string{"llm", "llm_with_tools", "verifier", "human", "finalize", "blocked"},
					},
				},
				"required":             []string{"route"},
				"additionalProperties": false,
			},
		},
		Resolve: func(spec dsl.GraphConditionSpec) (registry.EdgeCondition, error) {
			route := strings.TrimSpace(registry.StringConfigTrim(spec.Config, "route"))
			if route == "" {
				return registry.EdgeCondition{}, fmt.Errorf("execution_route_equals: route config is required")
			}
			return registry.NewEdgeCondition(dsl.GraphConditionSpec{
				Type: "execution_route_equals",
				Config: map[string]any{
					"route": route,
				},
			}, func(_ context.Context, state wfstate.State) bool {
				exec := state.Get(wfstate.StateKeyExecution)
				if exec == nil {
					return false
				}
				actual, _ := exec["route"].(string)
				return strings.EqualFold(actual, route)
			}), nil
		},
	}
}

func resolvePlanStepExecutorStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	plannerPath := strings.TrimSpace(registry.StringConfigTrim(spec.Config, "planner_state_path"))
	if plannerPath == "" {
		plannerPath = wfstate.StateKeyPlanner
	}
	plannerPath = canonicalContractPath(plannerPath)

	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{
				Path:        plannerPath + ".plan",
				Mode:        dsl.StateAccessReadWrite,
				Required:    true,
				Description: "Plan steps array.",
			},
			{
				Path:        plannerPath + ".current_step_id",
				Mode:        dsl.StateAccessWrite,
				Description: "ID of the step being executed.",
			},
			{
				Path:        plannerPath + ".status",
				Mode:        dsl.StateAccessRead,
				Description: "Current planner status.",
			},
			{
				Path:          canonicalContractPath(wfstate.StateKeyExecution),
				Mode:          dsl.StateAccessWrite,
				Required:      true,
				Description:   "Execution state: current step, route, step results.",
				MergeStrategy: dsl.StateMergeMerge,
			},
		},
	}, nil
}

func resolveObservationRecorderStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	scope := strings.TrimSpace(registry.StringConfigTrim(spec.Config, "state_scope"))
	if scope == "" {
		scope = "default"
	}

	plannerPath := strings.TrimSpace(registry.StringConfigTrim(spec.Config, "planner_state_path"))
	if plannerPath == "" {
		plannerPath = wfstate.StateKeyPlanner
	}
	plannerPath = canonicalContractPath(plannerPath)

	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{
				Path:        scopedStatePath(scope, "messages"),
				Mode:        dsl.StateAccessRead,
				Required:    true,
				Description: "Conversation messages to extract observations from.",
			},
			{
				Path:        plannerPath + ".current_step_id",
				Mode:        dsl.StateAccessRead,
				Description: "Current step ID for observation association.",
			},
			{
				Path:          canonicalContractPath(wfstate.StateKeyObservations),
				Mode:          dsl.StateAccessReadWrite,
				Description:   "Accumulated observations.",
				MergeStrategy: dsl.StateMergeAppend,
			},
			{
				Path:          canonicalContractPath(wfstate.StateKeyEvidence),
				Mode:          dsl.StateAccessReadWrite,
				Description:   "Accumulated evidence.",
				MergeStrategy: dsl.StateMergeAppend,
			},
			{
				Path:          canonicalContractPath(wfstate.StateKeyExecution + ".step_results"),
				Mode:          dsl.StateAccessReadWrite,
				Description:   "Per-step result records.",
				MergeStrategy: dsl.StateMergeMerge,
			},
		},
	}, nil
}

func canonicalContractPath(path string) string {
	return wfstate.NormalizeContractPath(path)
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
