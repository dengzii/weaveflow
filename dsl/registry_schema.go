package dsl

import "sort"

type NodeTypeSchema struct {
	Type              string                      `json:"type"`
	Title             string                      `json:"title,omitempty"`
	Description       string                      `json:"description,omitempty"`
	ConfigSchema      JSONSchema                  `json:"config_schema"`
	StatePorts        []StatePortDefinition       `json:"state_ports"`
	DynamicStatePorts *DynamicStatePortDefinition `json:"dynamic_state_ports,omitempty"`
}

type ConditionSchema struct {
	Type              string                      `json:"type"`
	Title             string                      `json:"title,omitempty"`
	Description       string                      `json:"description,omitempty"`
	ConfigSchema      JSONSchema                  `json:"config_schema"`
	StatePorts        []StatePortDefinition       `json:"state_ports"`
	DynamicStatePorts *DynamicStatePortDefinition `json:"dynamic_state_ports,omitempty"`
}

func BuildGraphDefinitionSchema(stateModules map[string]StateModuleDefinition, nodeTypes map[string]NodeTypeSchema, conditions map[string]ConditionSchema, reducerIDs ...string) JSONSchema {
	reducerItems := JSONSchema{"type": "string"}
	if len(reducerIDs) > 0 {
		reducerItems = JSONSchema{"enum": append([]string(nil), reducerIDs...)}
	}
	bindingSchema := JSONSchema{
		"type":                 "object",
		"properties":           JSONSchema{"path": JSONSchema{"type": "string"}, "reducer": reducerItems},
		"required":             []string{"path"},
		"additionalProperties": false,
	}
	executionPolicySchema := nodeExecutionPolicyJSONSchema()
	nodeVariants := make([]any, 0, len(nodeTypes))
	for _, key := range sortedNodeTypeSchemaKeys(nodeTypes) {
		nodeDef := nodeTypes[key]
		stateProperties := JSONSchema{}
		requiredState := make([]string, 0)
		for _, port := range nodeDef.StatePorts {
			stateProperties[port.Name] = bindingSchema
			if port.Required && port.DefaultPath == "" {
				requiredState = append(requiredState, port.Name)
			}
		}
		additionalProperties := any(false)
		if nodeDef.DynamicStatePorts != nil {
			additionalProperties = bindingSchema
		}
		stateSchema := JSONSchema{"type": "object", "properties": stateProperties, "additionalProperties": additionalProperties}
		if len(requiredState) > 0 {
			stateSchema["required"] = requiredState
		}
		requiredProperties := []string{"id", "type"}
		if len(requiredState) > 0 {
			requiredProperties = append(requiredProperties, "state")
		}
		nodeVariants = append(nodeVariants, JSONSchema{
			"type": "object",
			"properties": JSONSchema{
				"id":          JSONSchema{"type": "string"},
				"name":        JSONSchema{"type": "string"},
				"type":        JSONSchema{"const": nodeDef.Type},
				"description": JSONSchema{"type": "string"},
				"config":      nodeDef.ConfigSchema,
				"state":       stateSchema,
				"policy":      executionPolicySchema,
			},
			"required":             requiredProperties,
			"additionalProperties": false,
		})
	}

	conditionVariants := make([]any, 0, len(conditions))
	for _, key := range sortedConditionSchemaKeys(conditions) {
		conditionDef := conditions[key]
		stateProperties := JSONSchema{}
		requiredState := make([]string, 0)
		for _, port := range conditionDef.StatePorts {
			stateProperties[port.Name] = bindingSchema
			if port.Required && port.DefaultPath == "" {
				requiredState = append(requiredState, port.Name)
			}
		}
		additionalProperties := any(false)
		if conditionDef.DynamicStatePorts != nil {
			additionalProperties = bindingSchema
		}
		stateSchema := JSONSchema{"type": "object", "properties": stateProperties, "additionalProperties": additionalProperties}
		if len(requiredState) > 0 {
			stateSchema["required"] = requiredState
		}
		requiredProperties := []string{"type"}
		if len(requiredState) > 0 {
			requiredProperties = append(requiredProperties, "state")
		}
		conditionVariants = append(conditionVariants, JSONSchema{
			"type": "object",
			"properties": JSONSchema{
				"id":     JSONSchema{"type": "string"},
				"type":   JSONSchema{"const": conditionDef.Type},
				"config": conditionDef.ConfigSchema,
				"state":  stateSchema,
			},
			"required":             requiredProperties,
			"additionalProperties": false,
		})
	}

	moduleVariants := make([]any, 0, len(stateModules))
	for _, key := range sortedStateModuleDefinitionKeys(stateModules) {
		module := stateModules[key]
		moduleVariants = append(moduleVariants, JSONSchema{
			"type": "object",
			"properties": JSONSchema{
				"name":    JSONSchema{"const": module.Name},
				"version": JSONSchema{"const": module.Version},
			},
			"required":             []string{"name", "version"},
			"additionalProperties": false,
		})
	}

	return JSONSchema{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object",
		"properties": JSONSchema{
			"version":       JSONSchema{"const": GraphDefinitionVersion},
			"name":          JSONSchema{"type": "string"},
			"description":   JSONSchema{"type": "string"},
			"state_modules": JSONSchema{"type": "array", "minItems": 1, "items": JSONSchema{"oneOf": moduleVariants}},
			"entry_point":   JSONSchema{"type": "string"},
			"finish_point":  JSONSchema{"type": "string"},
			"nodes": JSONSchema{
				"type":  "array",
				"items": JSONSchema{"oneOf": nodeVariants},
			},
			"policy": graphExecutionPolicyJSONSchema(executionPolicySchema),
			"edges": JSONSchema{
				"type":        "array",
				"description": "Graph edges. Multiple ordinary edges with the same from node express fan-out; repeated from/to pairs are invalid. Conditional edges remain single-target branch selections.",
				"items": JSONSchema{
					"type": "object",
					"properties": JSONSchema{
						"from":      JSONSchema{"type": "string"},
						"to":        JSONSchema{"type": "string"},
						"condition": JSONSchema{"oneOf": conditionVariants},
						"failure": JSONSchema{
							"type": "object",
							"properties": JSONSchema{
								"stages":        JSONSchema{"type": "array", "items": JSONSchema{"enum": []string{string(FailureStageNode), string(FailureStageCondition)}}},
								"error_classes": JSONSchema{"type": "array", "items": JSONSchema{"type": "string"}},
								"catch_all":     JSONSchema{"type": "boolean"},
							},
							"additionalProperties": false,
						},
					},
					"required":             []string{"from", "to"},
					"additionalProperties": false,
				},
			},
			"metadata": JSONSchema{"type": "object"},
		},
		"required": []string{"version", "state_modules", "nodes"},
	}
}

func nodeExecutionPolicyJSONSchema() JSONSchema {
	retry := JSONSchema{
		"type": "object",
		"properties": JSONSchema{
			"max_attempts":                JSONSchema{"type": "integer", "minimum": 1},
			"initial_interval":            JSONSchema{"type": "string"},
			"max_interval":                JSONSchema{"type": "string"},
			"backoff_multiplier":          JSONSchema{"type": "number", "minimum": 1},
			"jitter":                      JSONSchema{"type": "number", "minimum": 0, "maximum": 1},
			"retryable_error_classes":     JSONSchema{"type": "array", "items": JSONSchema{"type": "string"}},
			"non_retryable_error_classes": JSONSchema{"type": "array", "items": JSONSchema{"type": "string"}},
		},
		"additionalProperties": false,
	}
	return JSONSchema{
		"type": "object",
		"properties": JSONSchema{
			"timeout":         JSONSchema{"type": "string"},
			"max_concurrency": JSONSchema{"type": "integer", "minimum": 1},
			"retry":           retry,
		},
		"additionalProperties": false,
	}
}

func graphExecutionPolicyJSONSchema(execution JSONSchema) JSONSchema {
	return JSONSchema{
		"type": "object",
		"properties": JSONSchema{
			"limits": JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"max_super_steps":      JSONSchema{"type": "integer", "minimum": 1},
					"max_node_executions":  JSONSchema{"type": "integer", "minimum": 1},
					"max_fan_out":          JSONSchema{"type": "integer", "minimum": 1},
					"max_concurrent_runs":  JSONSchema{"type": "integer", "minimum": 1},
					"max_concurrent_nodes": JSONSchema{"type": "integer", "minimum": 1},
					"max_concurrent_tools": JSONSchema{"type": "integer", "minimum": 1},
					"max_state_bytes":      JSONSchema{"type": "integer", "minimum": 1},
					"max_wall_time":        JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
			"node_defaults": execution,
		},
		"additionalProperties": false,
	}
}

func sortedStateModuleDefinitionKeys(input map[string]StateModuleDefinition) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedNodeTypeSchemaKeys(input map[string]NodeTypeSchema) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedConditionSchemaKeys(input map[string]ConditionSchema) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
