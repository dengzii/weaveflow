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

func TestGraphDefinitionSchemaAllowsDynamicStateBindings(t *testing.T) {
	t.Parallel()
	schema := BuildGraphDefinitionSchema(nil, map[string]NodeTypeSchema{
		"dynamic": {
			Type: "dynamic",
			DynamicStatePorts: &DynamicStatePortDefinition{
				NamePattern: "[A-Za-z_][A-Za-z0-9_]*", Schema: JSONSchema{"type": "object"},
				Mode: StateAccessRead, MergeStrategy: StateMergeReplace,
			},
		},
	}, nil)

	properties := schema["properties"].(JSONSchema)
	nodes := properties["nodes"].(JSONSchema)
	node := nodes["items"].(JSONSchema)
	variant := node["oneOf"].([]any)[0].(JSONSchema)
	stateSchema := variant["properties"].(JSONSchema)["state"].(JSONSchema)
	additional, ok := stateSchema["additionalProperties"].(JSONSchema)
	if !ok || additional["type"] != "object" {
		t.Fatalf("dynamic state additionalProperties = %#v", stateSchema["additionalProperties"])
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
