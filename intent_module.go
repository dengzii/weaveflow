package weaveflow

import (
	"fmt"
	"strings"
	"weaveflow/dsl"
	"weaveflow/nodes"
	fruntime "weaveflow/runtime"
)

func RegisterIntentModule(registry *Registry) {
	if registry == nil {
		return
	}

	registry.RegisterStateField(intentStateFieldDefinition())
	registry.RegisterNodeType(intentAnalyzerNodeTypeDefinition())
}

func intentStateFieldDefinition() StateFieldDefinition {
	return StateFieldDefinition{
		Name:        fruntime.StateKeyIntent,
		Description: "Structured intent analysis output for the current request.",
		Schema: JSONSchema{
			"type": "object",
			"properties": JSONSchema{
				"label":      JSONSchema{"type": "string"},
				"confidence": JSONSchema{"type": "number"},
				"reasoning":  JSONSchema{"type": "string"},
				"slots":      JSONSchema{"type": "object"},
				"candidates": JSONSchema{
					"type": "array",
					"items": JSONSchema{
						"type": "object",
						"properties": JSONSchema{
							"label":      JSONSchema{"type": "string"},
							"confidence": JSONSchema{"type": "number"},
							"reasoning":  JSONSchema{"type": "string"},
						},
						"additionalProperties": true,
					},
				},
			},
			"additionalProperties": true,
		},
	}
}

func intentAnalyzerNodeTypeDefinition() NodeTypeDefinition {
	stateSchema := intentStateFieldDefinition().Schema
	return NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "intent_analyzer",
			Title:       "Intent Analyzer Node",
			Description: "Analyze the current request and write a structured intent result into state.",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"intent_state_path": JSONSchema{"type": "string"},
					"input_path":        JSONSchema{"type": "string"},
					"state_scope":       JSONSchema{"type": "string"},
					"intent_options": JSONSchema{
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
		Build: func(ctx *BuildContext, spec dsl.GraphNodeSpec) (nodes.Node[State], error) {
			if ctx == nil || ctx.Model == nil {
				return nil, fmt.Errorf("build intent_analyzer nodes %q: model is required", spec.ID)
			}

			node := nodes.NewIntentAnalyzerNode(ctx.Model)
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
		},
		ResolveStateContract: resolveIntentAnalyzerStateContract,
	}
}

func resolveIntentAnalyzerStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	intentPath := strings.TrimSpace(stringConfig(spec.Config, "intent_state_path"))
	if intentPath == "" {
		intentPath = fruntime.StateKeyIntent
	}

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
				Path:        inputPath,
				Mode:        dsl.StateAccessRead,
				Required:    true,
				Description: "Explicit request input for intent analysis.",
			},
		}, contract.Fields...)
	}

	return contract, nil
}
