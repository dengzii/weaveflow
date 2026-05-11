package planning

import (
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
	registerIntentModule(registry)
	registerOrchestrationModule(registry)
	registerReplannerModule(registry)
}

func registerIntentModule(registry *registry.Registry) {
	registry.RegisterStateField(intentStateFieldDefinition())
	registry.RegisterNodeType(intentAnalyzerNodeTypeDefinition())
}

func registerOrchestrationModule(registry *registry.Registry) {
	registry.RegisterStateField(orchestrationStateFieldDefinition())
	registry.RegisterNodeType(orchestrationRouterNodeTypeDefinition())
}

func intentStateFieldDefinition() dsl.StateFieldDefinition {
	return dsl.StateFieldDefinition{
		Name:        fruntime.StateKeyIntent,
		Description: "Structured intent analysis output for the current request.",
		Schema: dsl.JSONSchema{
			"type": "object",
			"properties": dsl.JSONSchema{
				"label":      dsl.JSONSchema{"type": "string"},
				"confidence": dsl.JSONSchema{"type": "number"},
				"reasoning":  dsl.JSONSchema{"type": "string"},
				"slots":      dsl.JSONSchema{"type": "object"},
				"candidates": dsl.JSONSchema{
					"type": "array",
					"items": dsl.JSONSchema{
						"type": "object",
						"properties": dsl.JSONSchema{
							"label":      dsl.JSONSchema{"type": "string"},
							"confidence": dsl.JSONSchema{"type": "number"},
							"reasoning":  dsl.JSONSchema{"type": "string"},
						},
						"additionalProperties": true,
					},
				},
			},
			"additionalProperties": true,
		},
	}
}

func intentAnalyzerNodeTypeDefinition() registry.NodeTypeDefinition {
	stateSchema := intentStateFieldDefinition().Schema
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "intent_analyzer",
			Title:       "Intent Analyzer Node",
			Description: "Analyze the current request and write a structured intent result into state.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"intent_state_path": dsl.JSONSchema{"type": "string"},
					"input_path":        dsl.JSONSchema{"type": "string"},
					"state_scope":       dsl.JSONSchema{"type": "string"},
					"intent_options": dsl.JSONSchema{
						"type":  "array",
						"items": dsl.JSONSchema{"type": "string"},
					},
					"instructions": dsl.JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
			StateContract: &dsl.StateContract{
				Fields: []dsl.StateFieldRef{
					{
						Path:          "input_path",
						Mode:          dsl.StateAccessRead,
						Description:   "Optional explicit request input for intent analysis.",
						Dynamic:       true,
						PathConfigKey: "input_path",
					},
					{
						Path:          fruntime.StateKeyIntent,
						Mode:          dsl.StateAccessWrite,
						Required:      true,
						Description:   "Intent analysis output state subtree.",
						Schema:        stateSchema,
						Dynamic:       true,
						PathConfigKey: "intent_state_path",
						MergeStrategy: dsl.StateMergeMerge,
					},
				},
			},
		},
		Build: adaptLegacyNodeBuilder(func(ctx *registry.BuildContext, spec dsl.GraphNodeSpec) (core.Node[fruntime.State], error) {
			node := nodes.NewIntentAnalyzerNode()
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			node.IntentStatePath = stringConfig(spec.Config, "intent_state_path")
			node.InputPath = stringConfig(spec.Config, "input_path")
			node.StateScope = stringConfig(spec.Config, "state_scope")
			node.IntentOptions = stringSliceConfig(spec.Config, "intent_options")
			node.Instructions = stringConfig(spec.Config, "instructions")
			return node, nil
		}),
		ResolveStateContract: resolveIntentAnalyzerStateContract,
	}
}

func resolveIntentAnalyzerStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	intentPath := strings.TrimSpace(stringConfig(spec.Config, "intent_state_path"))
	if intentPath == "" {
		intentPath = fruntime.StateKeyIntent
	}
	intentPath = canonicalContractPath(intentPath)

	contract := dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{
				Path:          intentPath,
				Mode:          dsl.StateAccessWrite,
				Required:      true,
				Description:   "Intent analysis output state subtree.",
				Schema:        intentStateFieldDefinition().Schema,
				MergeStrategy: dsl.StateMergeMerge,
			},
		},
	}

	if inputPath := strings.TrimSpace(stringConfig(spec.Config, "input_path")); inputPath != "" {
		contract.Fields = append([]dsl.StateFieldRef{
			{
				Path:        canonicalContractPath(inputPath),
				Mode:        dsl.StateAccessRead,
				Required:    true,
				Description: "Explicit request input for intent analysis.",
			},
		}, contract.Fields...)
	}

	return contract, nil
}

func orchestrationStateFieldDefinition() dsl.StateFieldDefinition {
	return dsl.StateFieldDefinition{
		Name:        fruntime.StateKeyOrchestration,
		Description: "Structured orchestration routing decision for the current request.",
		Schema: dsl.JSONSchema{
			"type": "object",
			"properties": dsl.JSONSchema{
				"mode":                   dsl.JSONSchema{"type": "string"},
				"use_memory":             dsl.JSONSchema{"type": "boolean"},
				"memory_query":           dsl.JSONSchema{"type": "string"},
				"needs_clarification":    dsl.JSONSchema{"type": "boolean"},
				"clarification_question": dsl.JSONSchema{"type": "string"},
				"reasoning":              dsl.JSONSchema{"type": "string"},
				"target_subgraph":        dsl.JSONSchema{"type": "string"},
				"direct_answer":          dsl.JSONSchema{"type": "string"},
			},
			"additionalProperties": true,
		},
	}
}

func orchestrationRouterNodeTypeDefinition() registry.NodeTypeDefinition {
	stateSchema := orchestrationStateFieldDefinition().Schema
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "orchestration_router",
			Title:       "Orchestration Router Node",
			Description: "Route the current request into direct execution, planning, supervision, memory retrieval, or clarification.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"orchestration_state_path": dsl.JSONSchema{"type": "string"},
					"input_path":               dsl.JSONSchema{"type": "string"},
					"state_scope":              dsl.JSONSchema{"type": "string"},
					"context_paths": dsl.JSONSchema{
						"type":  "array",
						"items": dsl.JSONSchema{"type": "string"},
					},
					"available_modes": dsl.JSONSchema{
						"type":  "array",
						"items": dsl.JSONSchema{"type": "string"},
					},
					"instructions": dsl.JSONSchema{"type": "string"},
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
		Build: adaptLegacyNodeBuilder(func(ctx *registry.BuildContext, spec dsl.GraphNodeSpec) (core.Node[fruntime.State], error) {
			node := nodes.NewOrchestrationRouterNode()
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
		}),
		ResolveStateContract: resolveOrchestrationRouterStateContract,
	}
}

func resolveOrchestrationRouterStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	orchestrationPath := strings.TrimSpace(stringConfig(spec.Config, "orchestration_state_path"))
	if orchestrationPath == "" {
		orchestrationPath = fruntime.StateKeyOrchestration
	}
	orchestrationPath = canonicalContractPath(orchestrationPath)

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
				Path:        canonicalContractPath(inputPath),
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
				Path:        canonicalContractPath(path),
				Mode:        dsl.StateAccessRead,
				Description: "Optional orchestration context input.",
			},
		}, contract.Fields...)
	}

	return contract, nil
}

func registerReplannerModule(registry *registry.Registry) {
	registry.RegisterNodeType(replannerNodeTypeDefinition())
}

func replannerNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "replanner",
			Title:       "Replanner Node",
			Description: "Replan based on verification failures, preserving completed steps.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"planner_state_path": dsl.JSONSchema{"type": "string"},
					"context_paths": dsl.JSONSchema{
						"type":  "array",
						"items": dsl.JSONSchema{"type": "string"},
					},
					"max_steps": dsl.JSONSchema{"type": "integer", "minimum": 1},
					"step_kind_hints": dsl.JSONSchema{
						"type":  "array",
						"items": dsl.JSONSchema{"type": "string"},
					},
					"instructions": dsl.JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
		Build: adaptLegacyNodeBuilder(func(ctx *registry.BuildContext, spec dsl.GraphNodeSpec) (core.Node[fruntime.State], error) {
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
		}),
		ResolveStateContract: resolveReplannerStateContract,
	}
}

func resolveReplannerStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	plannerPath := strings.TrimSpace(stringConfig(spec.Config, "planner_state_path"))
	if plannerPath == "" {
		plannerPath = fruntime.StateKeyPlanner
	}
	plannerPath = canonicalContractPath(plannerPath)

	contract := dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{
				Path:        canonicalContractPath(fruntime.StateKeyVerification),
				Mode:        dsl.StateAccessRead,
				Description: "Verification issues triggering replan.",
			},
			{
				Path:        canonicalContractPath(fruntime.StateKeyObservations),
				Mode:        dsl.StateAccessRead,
				Description: "Observations including errors.",
			},
			{
				Path:        canonicalContractPath(fruntime.StateKeyExecution + ".step_results"),
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
			Path:        canonicalContractPath(path),
			Mode:        dsl.StateAccessRead,
			Description: "Replanner context input.",
		})
	}

	return contract, nil
}

func stringSliceConfig(config map[string]any, key string) []string {
	if len(config) == 0 {
		return nil
	}
	raw, ok := config[key]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case []string:
		out := make([]string, 0, len(typed))
		for _, value := range typed {
			value = strings.TrimSpace(value)
			if value != "" {
				out = append(out, value)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []any:
		out := make([]string, 0, len(typed))
		for _, value := range typed {
			text, _ := value.(string)
			text = strings.TrimSpace(text)
			if text != "" {
				out = append(out, text)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
	}
}

func stringConfig(config map[string]any, key string) string {
	if len(config) == 0 {
		return ""
	}
	value, _ := config[key].(string)
	return strings.TrimSpace(value)
}

func intConfig(config map[string]any, key string) (int, bool) {
	if len(config) == 0 {
		return 0, false
	}
	switch typed := config[key].(type) {
	case int:
		return typed, true
	case int8:
		return int(typed), true
	case int16:
		return int(typed), true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case float32:
		return int(typed), true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}

func canonicalContractPath(path string) string {
	return fruntime.NormalizeContractPath(path)
}
