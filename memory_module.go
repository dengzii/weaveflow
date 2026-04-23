package weaveflow

import (
	"strings"
	"weaveflow/dsl"
	"weaveflow/memory"
	"weaveflow/nodes"
	fruntime "weaveflow/runtime"
)

func RegisterMemoryModule(registry *Registry) {
	if registry == nil {
		return
	}

	registry.RegisterStateField(memoryStateFieldDefinition())
	registry.RegisterNodeType(memoryRecallNodeTypeDefinition())
	registry.RegisterNodeType(memoryWriteNodeTypeDefinition())
}

func memoryStateFieldDefinition() StateFieldDefinition {
	return StateFieldDefinition{
		Name:        fruntime.StateKeyMemory,
		Description: "Structured memory recall and memory write state for the current run.",
		Schema: JSONSchema{
			"type": "object",
			"properties": JSONSchema{
				"query": JSONSchema{"type": "object"},
				"recalled": JSONSchema{
					"type":  "array",
					"items": JSONSchema{"type": "object"},
				},
				"stats": JSONSchema{"type": "object"},
				"written": JSONSchema{
					"type":  "array",
					"items": JSONSchema{"type": "object"},
				},
				"write_stats": JSONSchema{"type": "object"},
			},
			"additionalProperties": true,
		},
	}
}

func memoryRecallNodeTypeDefinition() NodeTypeDefinition {
	return NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "memory_recall",
			Title:       "Memory Recall Node",
			Description: "Recall long-term memory into state using the configured memory manager.",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"memory_state_path":        JSONSchema{"type": "string"},
					"state_scope":              JSONSchema{"type": "string"},
					"query_path":               JSONSchema{"type": "string"},
					"request_input_path":       JSONSchema{"type": "string"},
					"orchestration_state_path": JSONSchema{"type": "string"},
					"limit":                    JSONSchema{"type": "integer", "minimum": 1},
					"roles": JSONSchema{
						"type":  "array",
						"items": JSONSchema{"type": "string"},
					},
					"tags": JSONSchema{
						"type":  "array",
						"items": JSONSchema{"type": "string"},
					},
					"types": JSONSchema{
						"type":  "array",
						"items": JSONSchema{"type": "string"},
					},
				},
				"additionalProperties": false,
			},
		},
		Build: func(ctx *BuildContext, spec dsl.GraphNodeSpec) (nodes.Node[State], error) {
			node := nodes.NewMemoryRecallNode(memoryManagerFromBuildContext(ctx))
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			node.StateScope = stringConfig(spec.Config, "state_scope")
			node.MemoryStatePath = stringConfig(spec.Config, "memory_state_path")
			node.QueryPath = stringConfig(spec.Config, "query_path")
			node.RequestInputPath = stringConfig(spec.Config, "request_input_path")
			node.OrchestrationStatePath = stringConfig(spec.Config, "orchestration_state_path")
			if value, ok := intConfig(spec.Config, "limit"); ok {
				node.Limit = value
			}
			node.Roles = stringSliceConfig(spec.Config, "roles")
			node.Tags = stringSliceConfig(spec.Config, "tags")
			node.Types = memoryEntryTypesConfig(spec.Config, "types")
			return node, nil
		},
		ResolveStateContract: resolveMemoryRecallStateContract,
	}
}

func memoryWriteNodeTypeDefinition() NodeTypeDefinition {
	return NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "memory_write",
			Title:       "Memory Write Node",
			Description: "Persist durable request and final-answer state into the configured memory manager.",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"memory_state_path":    JSONSchema{"type": "string"},
					"state_scope":          JSONSchema{"type": "string"},
					"request_input_path":   JSONSchema{"type": "string"},
					"final_answer_path":    JSONSchema{"type": "string"},
					"planner_state_path":   JSONSchema{"type": "string"},
					"include_request":      JSONSchema{"type": "boolean"},
					"include_final_answer": JSONSchema{"type": "boolean"},
					"include_summary":      JSONSchema{"type": "boolean"},
					"deduplicate":          JSONSchema{"type": "boolean"},
					"min_request_length":   JSONSchema{"type": "integer", "minimum": 1},
					"min_answer_length":    JSONSchema{"type": "integer", "minimum": 1},
					"min_summary_length":   JSONSchema{"type": "integer", "minimum": 1},
				},
				"additionalProperties": false,
			},
		},
		Build: func(ctx *BuildContext, spec dsl.GraphNodeSpec) (nodes.Node[State], error) {
			node := nodes.NewMemoryWriteNode(memoryManagerFromBuildContext(ctx))
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			node.MemoryStatePath = stringConfig(spec.Config, "memory_state_path")
			node.StateScope = stringConfig(spec.Config, "state_scope")
			node.RequestInputPath = stringConfig(spec.Config, "request_input_path")
			node.FinalAnswerPath = stringConfig(spec.Config, "final_answer_path")
			node.PlannerStatePath = stringConfig(spec.Config, "planner_state_path")
			if value, ok := boolConfig(spec.Config, "include_request"); ok {
				node.IncludeRequest = value
			}
			if value, ok := boolConfig(spec.Config, "include_final_answer"); ok {
				node.IncludeFinalAnswer = value
			}
			if value, ok := boolConfig(spec.Config, "include_summary"); ok {
				node.IncludeSummary = value
			}
			if value, ok := boolConfig(spec.Config, "deduplicate"); ok {
				node.Deduplicate = value
			}
			if value, ok := intConfig(spec.Config, "min_request_length"); ok {
				node.MinRequestLength = value
			}
			if value, ok := intConfig(spec.Config, "min_answer_length"); ok {
				node.MinAnswerLength = value
			}
			if value, ok := intConfig(spec.Config, "min_summary_length"); ok {
				node.MinSummaryLength = value
			}
			return node, nil
		},
		ResolveStateContract: resolveMemoryWriteStateContract,
	}
}

func resolveMemoryRecallStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	memoryPath := strings.TrimSpace(stringConfig(spec.Config, "memory_state_path"))
	if memoryPath == "" {
		memoryPath = fruntime.StateKeyMemory
	}

	contract := dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{
				Path:          memoryPath,
				Mode:          dsl.StateAccessWrite,
				Required:      true,
				Description:   "Memory recall output state subtree.",
				Schema:        memoryStateFieldDefinition().Schema,
				MergeStrategy: dsl.StateMergeMerge,
			},
		},
	}

	if queryPath := strings.TrimSpace(stringConfig(spec.Config, "query_path")); queryPath != "" {
		contract.Fields = append(contract.Fields, dsl.StateFieldRef{
			Path:        queryPath,
			Mode:        dsl.StateAccessRead,
			Description: "Optional explicit memory recall query.",
		})
	}

	requestInputPath := strings.TrimSpace(stringConfig(spec.Config, "request_input_path"))
	if requestInputPath == "" {
		requestInputPath = fruntime.StateKeyRequest + ".input"
	}
	contract.Fields = append(contract.Fields, dsl.StateFieldRef{
		Path:        requestInputPath,
		Mode:        dsl.StateAccessRead,
		Description: "Fallback request input used for memory recall when no explicit query is available.",
	})

	orchestrationPath := strings.TrimSpace(stringConfig(spec.Config, "orchestration_state_path"))
	if orchestrationPath == "" {
		orchestrationPath = fruntime.StateKeyOrchestration
	}
	contract.Fields = append(contract.Fields, dsl.StateFieldRef{
		Path:        orchestrationPath,
		Mode:        dsl.StateAccessRead,
		Description: "Orchestration state used to decide whether memory recall should run and which query to use.",
	})

	return contract, nil
}

func resolveMemoryWriteStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	memoryPath := strings.TrimSpace(stringConfig(spec.Config, "memory_state_path"))
	if memoryPath == "" {
		memoryPath = fruntime.StateKeyMemory
	}

	requestInputPath := strings.TrimSpace(stringConfig(spec.Config, "request_input_path"))
	if requestInputPath == "" {
		requestInputPath = fruntime.StateKeyRequest + ".input"
	}

	finalAnswerPath := strings.TrimSpace(stringConfig(spec.Config, "final_answer_path"))
	if finalAnswerPath == "" {
		finalAnswerPath = scopedConversationPath(stringConfig(spec.Config, "state_scope"), "final_answer")
	}

	return dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{
				Path:        requestInputPath,
				Mode:        dsl.StateAccessRead,
				Description: "Request input optionally written to memory.",
			},
			{
				Path:        finalAnswerPath,
				Mode:        dsl.StateAccessRead,
				Description: "Final answer optionally written to memory.",
			},
			{
				Path:          memoryPath,
				Mode:          dsl.StateAccessWrite,
				Required:      true,
				Description:   "Memory write output state subtree.",
				Schema:        memoryStateFieldDefinition().Schema,
				MergeStrategy: dsl.StateMergeMerge,
			},
		},
	}, nil
}

func memoryManagerFromBuildContext(ctx *BuildContext) memory.Manager {
	if ctx == nil {
		return nil
	}
	return ctx.Memory
}

func memoryEntryTypesConfig(config map[string]any, key string) []memory.EntryType {
	values := stringSliceConfig(config, key)
	if len(values) == 0 {
		return nil
	}
	result := make([]memory.EntryType, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		result = append(result, memory.EntryType(value))
	}
	return result
}
