package weaveflow

import (
	"context"
	"fmt"
	"strings"

	"weaveflow/dsl"
	"weaveflow/nodes"
	fruntime "weaveflow/runtime"
)

func RegisterExecutionModule(registry *Registry) {
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

func executionStateFieldDefinition() StateFieldDefinition {
	return StateFieldDefinition{
		Name:        fruntime.StateKeyExecution,
		Description: "Plan step execution state: current step, route decision, and step results.",
		Schema: JSONSchema{
			"type": "object",
			"properties": JSONSchema{
				"current_step": JSONSchema{"type": "object"},
				"route": JSONSchema{
					"type": "string",
					"enum": []string{"llm", "llm_with_tools", "verifier", "human", "finalize", "blocked"},
				},
				"step_results": JSONSchema{"type": "object"},
			},
			"additionalProperties": true,
		},
	}
}

func observationsStateFieldDefinition() StateFieldDefinition {
	return StateFieldDefinition{
		Name:        fruntime.StateKeyObservations,
		Description: "Structured observations recorded from tool and LLM outputs.",
		Schema: JSONSchema{
			"type": "array",
			"items": JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"source":     JSONSchema{"type": "string"},
					"summary":    JSONSchema{"type": "string"},
					"error":      JSONSchema{"type": "object"},
					"confidence": JSONSchema{"type": "number"},
					"step_id":    JSONSchema{"type": "string"},
					"timestamp":  JSONSchema{"type": "string"},
					"raw_ref":    JSONSchema{"type": "string"},
				},
			},
		},
	}
}

func evidenceStateFieldDefinition() StateFieldDefinition {
	return StateFieldDefinition{
		Name:        fruntime.StateKeyEvidence,
		Description: "Evidence entries collected from tool outputs and observations.",
		Schema: JSONSchema{
			"type": "array",
			"items": JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"type":         JSONSchema{"type": "string"},
					"content":      JSONSchema{"type": "string"},
					"source":       JSONSchema{"type": "string"},
					"artifact_ref": JSONSchema{"type": "string"},
				},
			},
		},
	}
}

func planStepExecutorNodeTypeDefinition() NodeTypeDefinition {
	return NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "plan_step_executor",
			Title:       "Plan Step Executor",
			Description: "Select the next ready step from the plan and route execution based on step kind.",
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
			node := nodes.NewPlanStepExecutorNode()
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
		ResolveStateContract: resolvePlanStepExecutorStateContract,
	}
}

func observationRecorderNodeTypeDefinition() NodeTypeDefinition {
	return NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "observation_recorder",
			Title:       "Observation Recorder",
			Description: "Record structured observations and evidence from recent tool and LLM outputs.",
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
			node := nodes.NewObservationRecorderNode()
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
		ResolveStateContract: resolveObservationRecorderStateContract,
	}
}

func executionRouteEqualsConditionDefinition() ConditionDefinition {
	return ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type:        "execution_route_equals",
			Title:       "Execution Route Equals",
			Description: "Routes based on the execution.route value set by plan_step_executor.",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"route": JSONSchema{
						"type": "string",
						"enum": []string{"llm", "llm_with_tools", "verifier", "human", "finalize", "blocked"},
					},
				},
				"required":             []string{"route"},
				"additionalProperties": false,
			},
		},
		Resolve: func(spec GraphConditionSpec) (EdgeCondition, error) {
			route := strings.TrimSpace(stringConfig(spec.Config, "route"))
			if route == "" {
				return EdgeCondition{}, fmt.Errorf("execution_route_equals: route config is required")
			}
			return NewEdgeCondition(GraphConditionSpec{
				Type: "execution_route_equals",
				Config: map[string]any{
					"route": route,
				},
			}, func(_ context.Context, state State) bool {
				exec := state.Get(fruntime.StateKeyExecution)
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
	plannerPath := strings.TrimSpace(stringConfig(spec.Config, "planner_state_path"))
	if plannerPath == "" {
		plannerPath = fruntime.StateKeyPlanner
	}

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
				Path:          fruntime.StateKeyExecution,
				Mode:          dsl.StateAccessWrite,
				Required:      true,
				Description:   "Execution state: current step, route, step results.",
				MergeStrategy: dsl.StateMergeMerge,
			},
		},
	}, nil
}

func resolveObservationRecorderStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
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
				Path:          fruntime.StateKeyObservations,
				Mode:          dsl.StateAccessReadWrite,
				Description:   "Accumulated observations.",
				MergeStrategy: dsl.StateMergeAppend,
			},
			{
				Path:          fruntime.StateKeyEvidence,
				Mode:          dsl.StateAccessReadWrite,
				Description:   "Accumulated evidence.",
				MergeStrategy: dsl.StateMergeAppend,
			},
			{
				Path:          fruntime.StateKeyExecution + ".step_results",
				Mode:          dsl.StateAccessReadWrite,
				Description:   "Per-step result records.",
				MergeStrategy: dsl.StateMergeMerge,
			},
		},
	}, nil
}
