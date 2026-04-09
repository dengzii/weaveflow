package weaveflow

import (
	"context"
	"fmt"
	"strings"
	"weaveflow/dsl"
	"weaveflow/nodes"
	fruntime "weaveflow/runtime"
)

func RegisterPlannerModule(registry *Registry) {
	if registry == nil {
		return
	}

	registry.RegisterStateField(plannerStateFieldDefinition())
	registry.RegisterNodeType(plannerNodeTypeDefinition())
	registry.RegisterCondition(plannerStatusEqualsConditionDefinition())
}

func plannerStateFieldDefinition() StateFieldDefinition {
	return StateFieldDefinition{
		Name:        fruntime.StateKeyPlanner,
		Description: "Structured plan state for generic task decomposition and execution tracking.",
		Schema: JSONSchema{
			"type": "object",
			"properties": JSONSchema{
				"objective":       JSONSchema{"type": "string"},
				"status":          JSONSchema{"type": "string"},
				"current_step_id": JSONSchema{"type": "string"},
				"summary":         JSONSchema{"type": "string"},
				"replan_reason":   JSONSchema{"type": "string"},
				"plan": JSONSchema{
					"type": "array",
					"items": JSONSchema{
						"type": "object",
						"properties": JSONSchema{
							"id":                  JSONSchema{"type": "string"},
							"title":               JSONSchema{"type": "string"},
							"description":         JSONSchema{"type": "string"},
							"status":              JSONSchema{"type": "string"},
							"kind":                JSONSchema{"type": "string"},
							"depends_on":          JSONSchema{"type": "array", "items": JSONSchema{"type": "string"}},
							"acceptance_criteria": JSONSchema{"type": "array", "items": JSONSchema{"type": "string"}},
							"outputs":             JSONSchema{"type": "array", "items": JSONSchema{"type": "string"}},
						},
						"additionalProperties": true,
					},
				},
				"metadata": JSONSchema{"type": "object"},
			},
			"additionalProperties": true,
		},
	}
}

func plannerNodeTypeDefinition() NodeTypeDefinition {
	stateSchema := plannerStateFieldDefinition().Schema
	return NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "planner",
			Title:       "Planner Node",
			Description: "Generate or refresh a structured task plan from the current objective and state context.",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"planner_state_path": JSONSchema{"type": "string"},
					"objective_path":     JSONSchema{"type": "string"},
					"context_paths": JSONSchema{
						"type":  "array",
						"items": JSONSchema{"type": "string"},
					},
					"max_steps": JSONSchema{"type": "integer", "minimum": 1},
					"step_kind_hints": JSONSchema{
						"type":  "array",
						"items": JSONSchema{"type": "string"},
					},
					"instructions": JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
			StateContract: &dsl.StateContract{
				Fields: []dsl.StateFieldRef{
					{
						Path:          "objective_path",
						Mode:          dsl.StateAccessRead,
						Required:      true,
						Description:   "Planner objective input.",
						Dynamic:       true,
						PathConfigKey: "objective_path",
					},
					{
						Path:          "context_paths[]",
						Mode:          dsl.StateAccessRead,
						Description:   "Optional planner context inputs.",
						Dynamic:       true,
						PathConfigKey: "context_paths",
					},
					{
						Path:          fruntime.StateKeyPlanner,
						Mode:          dsl.StateAccessWrite,
						Required:      true,
						Description:   "Planner output state subtree.",
						Schema:        stateSchema,
						Dynamic:       true,
						PathConfigKey: "planner_state_path",
						MergeStrategy: dsl.StateMergeMerge,
					},
				},
			},
		},
		Build: func(ctx *BuildContext, spec dsl.GraphNodeSpec) (nodes.Node[State], error) {
			if ctx == nil || ctx.Model == nil {
				return nil, fmt.Errorf("build planner nodes %q: model is required", spec.ID)
			}

			node := nodes.NewPlannerNode(ctx.Model)
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			node.PlannerStatePath = stringConfig(spec.Config, "planner_state_path")
			node.ObjectivePath = stringConfig(spec.Config, "objective_path")
			node.ContextPaths = stringSliceConfig(spec.Config, "context_paths")
			node.StepKindHints = stringSliceConfig(spec.Config, "step_kind_hints")
			node.Instructions = stringConfig(spec.Config, "instructions")
			if value, ok := intConfig(spec.Config, "max_steps"); ok {
				node.MaxSteps = value
			}
			return node, nil
		},
		ResolveStateContract: resolvePlannerStateContract,
	}
}

func plannerStatusEqualsConditionDefinition() ConditionDefinition {
	return ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type:        "planner_status_equals",
			Title:       "Planner Status Equals",
			Description: "Routes when the planner status matches the configured value.",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"planner_state_path": JSONSchema{"type": "string"},
					"status":             JSONSchema{"type": "string"},
				},
				"required":             []string{"status"},
				"additionalProperties": false,
			},
		},
		Resolve: func(spec GraphConditionSpec) (EdgeCondition, error) {
			expected := strings.ToLower(strings.TrimSpace(stringConfig(spec.Config, "status")))
			if expected == "" {
				return EdgeCondition{}, fmt.Errorf("planner status is required")
			}
			plannerPath := strings.TrimSpace(stringConfig(spec.Config, "planner_state_path"))
			if plannerPath == "" {
				plannerPath = fruntime.StateKeyPlanner
			}

			return NewEdgeCondition(GraphConditionSpec{
				Type: "planner_status_equals",
				Config: map[string]any{
					"planner_state_path": plannerPath,
					"status":             expected,
				},
			}, func(_ context.Context, state State) bool {
				value, ok := fruntime.ResolveStatePath(state, plannerPath+".status")
				if !ok {
					return false
				}
				status, ok := value.(string)
				if !ok {
					return false
				}
				return strings.ToLower(strings.TrimSpace(status)) == expected
			}), nil
		},
	}
}

func resolvePlannerStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	plannerPath := strings.TrimSpace(stringConfig(spec.Config, "planner_state_path"))
	if plannerPath == "" {
		plannerPath = fruntime.StateKeyPlanner
	}

	objectivePath := strings.TrimSpace(stringConfig(spec.Config, "objective_path"))
	if objectivePath == "" {
		objectivePath = plannerPath + ".objective"
	}

	contract := dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{
				Path:        objectivePath,
				Mode:        dsl.StateAccessRead,
				Required:    true,
				Description: "Planner objective input.",
			},
			{
				Path:          plannerPath,
				Mode:          dsl.StateAccessWrite,
				Required:      true,
				Description:   "Planner output state subtree.",
				Schema:        plannerStateFieldDefinition().Schema,
				MergeStrategy: dsl.StateMergeMerge,
			},
		},
	}

	for _, path := range stringSliceConfig(spec.Config, "context_paths") {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		contract.Fields = append(contract.Fields, dsl.StateFieldRef{
			Path:        path,
			Mode:        dsl.StateAccessRead,
			Description: "Planner context input.",
		})
	}

	return contract, nil
}
