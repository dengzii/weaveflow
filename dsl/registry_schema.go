package dsl

import "sort"

type NodeTypeSchema struct {
	Type          string         `json:"type"`
	Title         string         `json:"title,omitempty"`
	Description   string         `json:"description,omitempty"`
	ConfigSchema  JSONSchema     `json:"config_schema"`
	StateContract *StateContract `json:"state_contract,omitempty"`
}

type ConditionSchema struct {
	Type         string     `json:"type"`
	Title        string     `json:"title,omitempty"`
	Description  string     `json:"description,omitempty"`
	ConfigSchema JSONSchema `json:"config_schema"`
}

func BuildGraphDefinitionSchema(stateSchemaID string, stateFields map[string]StateFieldDefinition, nodeTypes map[string]NodeTypeSchema, conditions map[string]ConditionSchema) JSONSchema {
	nodeVariants := make([]any, 0, len(nodeTypes))
	for _, key := range sortedNodeTypeSchemaKeys(nodeTypes) {
		nodeDef := nodeTypes[key]
		nodeVariants = append(nodeVariants, JSONSchema{
			"type": "object",
			"properties": JSONSchema{
				"id":          JSONSchema{"type": "string"},
				"name":        JSONSchema{"type": "string"},
				"type":        JSONSchema{"const": nodeDef.Type},
				"description": JSONSchema{"type": "string"},
				"config":      nodeDef.ConfigSchema,
			},
			"required":             []string{"id", "type"},
			"additionalProperties": false,
		})
	}

	conditionVariants := make([]any, 0, len(conditions))
	for _, key := range sortedConditionSchemaKeys(conditions) {
		conditionDef := conditions[key]
		conditionVariants = append(conditionVariants, JSONSchema{
			"type": "object",
			"properties": JSONSchema{
				"type":   JSONSchema{"const": conditionDef.Type},
				"config": conditionDef.ConfigSchema,
			},
			"required":             []string{"type"},
			"additionalProperties": false,
		})
	}

	stateFieldSchemas := JSONSchema{}
	for _, key := range sortedStateFieldDefinitionKeys(stateFields) {
		field := stateFields[key]
		stateFieldSchemas[field.Name] = field.Schema
	}

	return JSONSchema{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object",
		"properties": JSONSchema{
			"version":      JSONSchema{"type": "string"},
			"name":         JSONSchema{"type": "string"},
			"description":  JSONSchema{"type": "string"},
			"state_schema": JSONSchema{"const": stateSchemaID},
			"entry_point":  JSONSchema{"type": "string"},
			"finish_point": JSONSchema{"type": "string"},
			"nodes": JSONSchema{
				"type":  "array",
				"items": JSONSchema{"oneOf": nodeVariants},
			},
			"edges": JSONSchema{
				"type": "array",
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
		"required": []string{"nodes"},
		"$defs": JSONSchema{
			"common_state": JSONSchema{
				"type":                 "object",
				"properties":           stateFieldSchemas,
				"additionalProperties": true,
			},
		},
	}
}

func sortedStateFieldDefinitionKeys(input map[string]StateFieldDefinition) []string {
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
