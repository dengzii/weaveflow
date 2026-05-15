package builtin

import (
	"strings"

	"weaveflow/core"
	"weaveflow/dsl"
	wfmemory "weaveflow/memory"
	"weaveflow/nodes"
	"weaveflow/registry"
	wfstate "weaveflow/state"
)

func registerMemoryModule(registry *registry.Registry) {
	if registry == nil {
		return
	}
	registry.RegisterStateField(memoryStateFieldDefinition())
	registry.RegisterNodeType(memoryRecallNodeTypeDefinition())
	registry.RegisterNodeType(memoryWriteNodeTypeDefinition())
}

func memoryStateFieldDefinition() dsl.StateFieldDefinition {
	return dsl.StateFieldDefinition{
		Name:        wfstate.StateKeyMemory,
		Description: "Structured memory recall and memory write state for the current run.",
		Schema: dsl.JSONSchema{
			"type": "object",
			"properties": dsl.JSONSchema{
				"query": dsl.JSONSchema{"type": "object"},
				"recalled": dsl.JSONSchema{
					"type":  "array",
					"items": dsl.JSONSchema{"type": "object"},
				},
				"stats": dsl.JSONSchema{"type": "object"},
				"written": dsl.JSONSchema{
					"type":  "array",
					"items": dsl.JSONSchema{"type": "object"},
				},
				"write_stats": dsl.JSONSchema{"type": "object"},
			},
			"additionalProperties": true,
		},
	}
}

func memoryRecallNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "memory_recall",
			Title:       "Memory Recall Node",
			Description: "Recall long-term memory into state using the configured memory manager.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"memory_state_path":        dsl.JSONSchema{"type": "string"},
					"state_scope":              dsl.JSONSchema{"type": "string"},
					"query_path":               dsl.JSONSchema{"type": "string"},
					"request_input_path":       dsl.JSONSchema{"type": "string"},
					"orchestration_state_path": dsl.JSONSchema{"type": "string"},
					"limit":                    dsl.JSONSchema{"type": "integer", "minimum": 1},
					"roles": dsl.JSONSchema{
						"type":  "array",
						"items": dsl.JSONSchema{"type": "string"},
					},
					"tags": dsl.JSONSchema{
						"type":  "array",
						"items": dsl.JSONSchema{"type": "string"},
					},
					"types": dsl.JSONSchema{
						"type":  "array",
						"items": dsl.JSONSchema{"type": "string"},
					},
				},
				"additionalProperties": false,
			},
		},
		Build: adaptNodeBuilder(func(ctx *registry.BuildContext, spec dsl.GraphNodeSpec) (core.Node[wfstate.State, wfstate.StatePatch], error) {
			node := nodes.NewMemoryRecallNode()
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			node.StateScope = registry.StringConfigTrim(spec.Config, "state_scope")
			node.MemoryStatePath = registry.StringConfigTrim(spec.Config, "memory_state_path")
			node.QueryPath = registry.StringConfigTrim(spec.Config, "query_path")
			node.RequestInputPath = registry.StringConfigTrim(spec.Config, "request_input_path")
			node.OrchestrationStatePath = registry.StringConfigTrim(spec.Config, "orchestration_state_path")
			if value, ok := registry.IntConfig(spec.Config, "limit"); ok {
				node.Limit = value
			}
			node.Roles = registry.StringSliceConfigTrim(spec.Config, "roles")
			node.Tags = registry.StringSliceConfigTrim(spec.Config, "tags")
			node.Types = memoryEntryTypesConfig(spec.Config, "types")
			return node, nil
		}),
		ResolveStateContract: resolveMemoryRecallStateContract,
	}
}

func memoryWriteNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "memory_write",
			Title:       "Memory Write Node",
			Description: "Persist durable request and final-answer state into the configured memory manager.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"memory_state_path":    dsl.JSONSchema{"type": "string"},
					"state_scope":          dsl.JSONSchema{"type": "string"},
					"request_input_path":   dsl.JSONSchema{"type": "string"},
					"final_answer_path":    dsl.JSONSchema{"type": "string"},
					"planner_state_path":   dsl.JSONSchema{"type": "string"},
					"include_request":      dsl.JSONSchema{"type": "boolean"},
					"include_final_answer": dsl.JSONSchema{"type": "boolean"},
					"include_summary":      dsl.JSONSchema{"type": "boolean"},
					"deduplicate":          dsl.JSONSchema{"type": "boolean"},
					"min_request_length":   dsl.JSONSchema{"type": "integer", "minimum": 1},
					"min_answer_length":    dsl.JSONSchema{"type": "integer", "minimum": 1},
					"min_summary_length":   dsl.JSONSchema{"type": "integer", "minimum": 1},
				},
				"additionalProperties": false,
			},
		},
		Build: adaptNodeBuilder(func(ctx *registry.BuildContext, spec dsl.GraphNodeSpec) (core.Node[wfstate.State, wfstate.StatePatch], error) {
			node := nodes.NewMemoryWriteNode()
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			node.MemoryStatePath = registry.StringConfigTrim(spec.Config, "memory_state_path")
			node.StateScope = registry.StringConfigTrim(spec.Config, "state_scope")
			node.RequestInputPath = registry.StringConfigTrim(spec.Config, "request_input_path")
			node.FinalAnswerPath = registry.StringConfigTrim(spec.Config, "final_answer_path")
			node.PlannerStatePath = registry.StringConfigTrim(spec.Config, "planner_state_path")
			if value, ok := registry.BoolConfig(spec.Config, "include_request"); ok {
				node.IncludeRequest = value
			}
			if value, ok := registry.BoolConfig(spec.Config, "include_final_answer"); ok {
				node.IncludeFinalAnswer = value
			}
			if value, ok := registry.BoolConfig(spec.Config, "include_summary"); ok {
				node.IncludeSummary = value
			}
			if value, ok := registry.BoolConfig(spec.Config, "deduplicate"); ok {
				node.Deduplicate = value
			}
			if value, ok := registry.IntConfig(spec.Config, "min_request_length"); ok {
				node.MinRequestLength = value
			}
			if value, ok := registry.IntConfig(spec.Config, "min_answer_length"); ok {
				node.MinAnswerLength = value
			}
			if value, ok := registry.IntConfig(spec.Config, "min_summary_length"); ok {
				node.MinSummaryLength = value
			}
			return node, nil
		}),
		ResolveStateContract: resolveMemoryWriteStateContract,
	}
}

func resolveMemoryRecallStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	memoryPath := strings.TrimSpace(registry.StringConfigTrim(spec.Config, "memory_state_path"))
	if memoryPath == "" {
		memoryPath = wfstate.StateKeyMemory
	}
	memoryPath = canonicalContractPath(memoryPath)

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

	if queryPath := strings.TrimSpace(registry.StringConfigTrim(spec.Config, "query_path")); queryPath != "" {
		contract.Fields = append(contract.Fields, dsl.StateFieldRef{
			Path:        canonicalContractPath(queryPath),
			Mode:        dsl.StateAccessRead,
			Description: "Optional explicit memory recall query.",
		})
	}

	requestInputPath := strings.TrimSpace(registry.StringConfigTrim(spec.Config, "request_input_path"))
	if requestInputPath == "" {
		requestInputPath = wfstate.StateKeyRequest + ".input"
	}
	requestInputPath = canonicalContractPath(requestInputPath)
	contract.Fields = append(contract.Fields, dsl.StateFieldRef{
		Path:        requestInputPath,
		Mode:        dsl.StateAccessRead,
		Description: "Fallback request input used for memory recall when no explicit query is available.",
	})

	orchestrationPath := strings.TrimSpace(registry.StringConfigTrim(spec.Config, "orchestration_state_path"))
	if orchestrationPath == "" {
		orchestrationPath = wfstate.StateKeyOrchestration
	}
	orchestrationPath = canonicalContractPath(orchestrationPath)
	contract.Fields = append(contract.Fields, dsl.StateFieldRef{
		Path:        orchestrationPath,
		Mode:        dsl.StateAccessRead,
		Description: "Orchestration state used to decide whether memory recall should run and which query to use.",
	})

	contract.Fields = append(contract.Fields, dsl.StateFieldRef{
		Path:        scopedConversationPath(registry.StringConfigTrim(spec.Config, "state_scope"), "messages"),
		Mode:        dsl.StateAccessRead,
		Description: "Conversation messages used as a last-resort recall query source.",
	})

	return contract, nil
}

func resolveMemoryWriteStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	memoryPath := strings.TrimSpace(registry.StringConfigTrim(spec.Config, "memory_state_path"))
	if memoryPath == "" {
		memoryPath = wfstate.StateKeyMemory
	}
	memoryPath = canonicalContractPath(memoryPath)

	requestInputPath := strings.TrimSpace(registry.StringConfigTrim(spec.Config, "request_input_path"))
	if requestInputPath == "" {
		requestInputPath = wfstate.StateKeyRequest + ".input"
	}
	requestInputPath = canonicalContractPath(requestInputPath)

	finalAnswerPath := strings.TrimSpace(registry.StringConfigTrim(spec.Config, "final_answer_path"))
	if finalAnswerPath == "" {
		finalAnswerPath = scopedConversationPath(registry.StringConfigTrim(spec.Config, "state_scope"), "final_answer")
	}
	finalAnswerPath = canonicalContractPath(finalAnswerPath)

	plannerPath := strings.TrimSpace(registry.StringConfigTrim(spec.Config, "planner_state_path"))
	if plannerPath == "" {
		plannerPath = wfstate.StateKeyPlanner
	}
	plannerPath = canonicalContractPath(plannerPath)

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
				Path:        plannerPath,
				Mode:        dsl.StateAccessRead,
				Description: "Planner state used to derive the persisted run summary.",
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

func memoryEntryTypesConfig(config map[string]any, key string) []wfmemory.EntryType {
	values := registry.StringSliceConfigTrim(config, key)
	if len(values) == 0 {
		return nil
	}
	result := make([]wfmemory.EntryType, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		result = append(result, wfmemory.EntryType(value))
	}
	return result
}
