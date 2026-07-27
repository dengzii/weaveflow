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

func BuildGraphDefinitionSchema(stateModules map[string]StateModuleDefinition, nodeTypes map[string]NodeTypeSchema, conditions map[string]ConditionSchema) JSONSchema {
	bindingSchema := JSONSchema{
		"type":                 "object",
		"properties":           JSONSchema{"path": JSONSchema{"type": "string"}},
		"required":             []string{"path"},
		"additionalProperties": false,
	}
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
			"edges": JSONSchema{
				"type":        "array",
				"description": "Graph edges. Multiple ordinary edges with the same from node express fan-out; repeated from/to pairs are invalid. Conditional edges remain single-target branch selections.",
				"items": JSONSchema{
					"type": "object",
					"properties": JSONSchema{
						"from":      JSONSchema{"type": "string"},
						"to":        JSONSchema{"type": "string"},
						"condition": JSONSchema{"oneOf": conditionVariants},
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
