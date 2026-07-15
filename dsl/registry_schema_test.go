package dsl

import "testing"

func TestGraphDefinitionSchemaRequiresStateForConditionWithRequiredPort(t *testing.T) {
	t.Parallel()
	schema := BuildGraphDefinitionSchema(nil, nil, map[string]ConditionSchema{
		"required": {
			Type: "required",
			StatePorts: []StatePortDefinition{{
				Name: "value", Required: true, Schema: JSONSchema{"type": "string"}, Mode: StateAccessRead, MergeStrategy: StateMergeReplace,
			}},
		},
	})

	properties := schema["properties"].(JSONSchema)
	edges := properties["edges"].(JSONSchema)
	edge := edges["items"].(JSONSchema)
	edgeProperties := edge["properties"].(JSONSchema)
	condition := edgeProperties["condition"].(JSONSchema)
	variants := condition["oneOf"].([]any)
	variant := variants[0].(JSONSchema)
	required := variant["required"].([]string)
	if !containsString(required, "state") {
		t.Fatalf("required condition state is missing from schema: %#v", variant)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
