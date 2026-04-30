package weaveflow

import (
	"strings"
	"weaveflow/dsl"
	"weaveflow/nodes"
	fruntime "weaveflow/runtime"
)

func RegisterReplannerModule(registry *Registry) {
	if registry == nil {
		return
	}

	registry.RegisterNodeType(replannerNodeTypeDefinition())
}

func replannerNodeTypeDefinition() NodeTypeDefinition {
	return NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "replanner",
			Title:       "Replanner Node",
			Description: "Replan based on verification failures, preserving completed steps.",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"planner_state_path": JSONSchema{"type": "string"},
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
		},
		Build: func(ctx *BuildContext, spec dsl.GraphNodeSpec) (nodes.Node[State], error) {
			node := nodes.NewReplannerNode()
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			node.PlannerStatePath = stringConfig(spec.Config, "planner_state_path")
			node.ContextPaths = stringSliceConfig(spec.Config, "context_paths")
			node.StepKindHints = stringSliceConfig(spec.Config, "step_kind_hints")
			node.Instructions = stringConfig(spec.Config, "instructions")
			if value, ok := intConfig(spec.Config, "max_steps"); ok {
				node.MaxSteps = value
			}
			return node, nil
		},
		ResolveStateContract: resolveReplannerStateContract,
	}
}

func resolveReplannerStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	plannerPath := strings.TrimSpace(stringConfig(spec.Config, "planner_state_path"))
	if plannerPath == "" {
		plannerPath = fruntime.StateKeyPlanner
	}

	contract := dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{
				Path:        fruntime.StateKeyVerification,
				Mode:        dsl.StateAccessRead,
				Description: "Verification issues triggering replan.",
			},
			{
				Path:        fruntime.StateKeyObservations,
				Mode:        dsl.StateAccessRead,
				Description: "Observations including errors.",
			},
			{
				Path:        fruntime.StateKeyExecution + ".step_results",
				Mode:        dsl.StateAccessRead,
				Description: "Step execution results.",
			},
			{
				Path:          plannerPath,
				Mode:          dsl.StateAccessReadWrite,
				Required:      true,
				Description:   "Planner state updated with replan.",
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
			Description: "Replanner context input.",
		})
	}

	return contract, nil
}
