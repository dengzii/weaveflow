package dsl

import "testing"

func TestStateContractCloneDeepCopiesSchemas(t *testing.T) {
	t.Parallel()

	contract := StateContract{
		Fields: []StateFieldRef{
			{
				Path:        "planner",
				Mode:        StateAccessWrite,
				Description: "Planner state subtree.",
				Schema: JSONSchema{
					"type": "object",
					"properties": JSONSchema{
						"status": JSONSchema{"type": "string"},
					},
				},
			},
		},
	}

	cloned := contract.Clone()
	if len(cloned.Fields) != 1 {
		t.Fatalf("expected one cloned field, got %#v", cloned.Fields)
	}

	properties, ok := cloned.Fields[0].Schema["properties"].(JSONSchema)
	if !ok {
		t.Fatalf("expected cloned schema properties to be present, got %#v", cloned.Fields[0].Schema)
	}
	properties["status"] = JSONSchema{"type": "integer"}

	originalProperties, ok := contract.Fields[0].Schema["properties"].(JSONSchema)
	if !ok {
		t.Fatalf("expected original schema properties to remain JSONSchema, got %#v", contract.Fields[0].Schema)
	}
	statusSchema, ok := originalProperties["status"].(JSONSchema)
	if !ok || statusSchema["type"] != "string" {
		t.Fatalf("expected original schema to remain unchanged, got %#v", originalProperties["status"])
	}
}
