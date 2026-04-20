package weaveflow

import (
	"fmt"
	"strings"
	"weaveflow/dsl"
	"weaveflow/nodes"
	fruntime "weaveflow/runtime"
)

func RegisterOrchestrationModule(registry *Registry) {
	if registry == nil {
		return
	}

	registry.RegisterStateField(orchestrationStateFieldDefinition())
	registry.RegisterNodeType(orchestrationRouterNodeTypeDefinition())
}

func orchestrationStateFieldDefinition() StateFieldDefinition {
	return StateFieldDefinition{
		Name:        fruntime.StateKeyOrchestration,
		Description: "Structured orchestration routing decision for the current request.",
		Schema: JSONSchema{
			"type": "object",
			"properties": JSONSchema{
				"mode":                   JSONSchema{"type": "string"},
				"use_memory":             JSONSchema{"type": "boolean"},
				"memory_query":           JSONSchema{"type": "string"},
				"needs_clarification":    JSONSchema{"type": "boolean"},
				"clarification_question": JSONSchema{"type": "string"},
				"reasoning":              JSONSchema{"type": "string"},
				"target_subgraph":        JSONSchema{"type": "string"},
			},
			"additionalProperties": true,
		},
	}
}

func orchestrationRouterNodeTypeDefinition() NodeTypeDefinition {
	stateSchema := orchestrationStateFieldDefinition().Schema
	return NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "orchestration_router",
			Title:       "Orchestration Router Node",
			Description: "Route the current request into direct execution, planning, supervision, memory retrieval, or clarification.",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"orchestration_state_path": JSONSchema{"type": "string"},
					"input_path":               JSONSchema{"type": "string"},
					"state_scope":              JSONSchema{"type": "string"},
					"context_paths": JSONSchema{
						"type":  "array",
						"items": JSONSchema{"type": "string"},
					},
					"available_modes": JSONSchema{
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
						Path:          "input_path",
						Mode:          dsl.StateAccessRead,
						Description:   "Optional explicit request input for orchestration routing.",
						Dynamic:       true,
						PathConfigKey: "input_path",
					},
					{
						Path:          fruntime.StateKeyOrchestration,
						Mode:          dsl.StateAccessWrite,
						Required:      true,
						Description:   "Orchestration routing output state subtree.",
						Schema:        stateSchema,
						Dynamic:       true,
						PathConfigKey: "orchestration_state_path",
						MergeStrategy: dsl.StateMergeMerge,
					},
				},
			},
		},
		Build: func(ctx *BuildContext, spec dsl.GraphNodeSpec) (nodes.Node[State], error) {
			if ctx == nil || ctx.Model == nil {
				return nil, fmt.Errorf("build orchestration_router node %q: model is required", spec.ID)
			}

			node := nodes.NewOrchestrationRouterNode(ctx.Model)
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			node.OrchestrationStatePath = stringConfig(spec.Config, "orchestration_state_path")
			node.InputPath = stringConfig(spec.Config, "input_path")
			node.StateScope = stringConfig(spec.Config, "state_scope")
			node.ContextPaths = stringSliceConfig(spec.Config, "context_paths")
			node.AvailableModes = stringSliceConfig(spec.Config, "available_modes")
			node.Instructions = stringConfig(spec.Config, "instructions")
			return node, nil
		},
		ResolveStateContract: resolveOrchestrationRouterStateContract,
	}
}

func resolveOrchestrationRouterStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	orchestrationPath := strings.TrimSpace(stringConfig(spec.Config, "orchestration_state_path"))
	if orchestrationPath == "" {
		orchestrationPath = fruntime.StateKeyOrchestration
	}

	contract := dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{
				Path:          orchestrationPath,
				Mode:          dsl.StateAccessWrite,
				Required:      true,
				Description:   "Orchestration routing output state subtree.",
				Schema:        orchestrationStateFieldDefinition().Schema,
				MergeStrategy: dsl.StateMergeMerge,
			},
		},
	}

	if inputPath := strings.TrimSpace(stringConfig(spec.Config, "input_path")); inputPath != "" {
		contract.Fields = append([]dsl.StateFieldRef{
			{
				Path:        inputPath,
				Mode:        dsl.StateAccessRead,
				Required:    true,
				Description: "Explicit request input for orchestration routing.",
			},
		}, contract.Fields...)
	}

	for _, path := range stringSliceConfig(spec.Config, "context_paths") {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		contract.Fields = append([]dsl.StateFieldRef{
			{
				Path:        path,
				Mode:        dsl.StateAccessRead,
				Description: "Optional orchestration context input.",
			},
		}, contract.Fields...)
	}

	return contract, nil
}
