package runtime

type StateFieldDefinition struct {
	Name        string
	Description string
	Schema      map[string]any
}

func DefaultStateFieldDefinitions() []StateFieldDefinition {
	return []StateFieldDefinition{
		{
			Name:        StateKeyMessages,
			Description: "Chat messages accumulated during the graph run.",
			Schema: map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
				},
			},
		},
		{
			Name:        StateKeyIterationCount,
			Description: "Current tool-using iteration count.",
			Schema:      map[string]any{"type": "integer", "minimum": 0},
		},
		{
			Name:        StateKeyMaxIterations,
			Description: "Maximum iteration count allowed for the run.",
			Schema:      map[string]any{"type": "integer", "minimum": 1},
		},
		{
			Name:        StateKeyFinalAnswer,
			Description: "Final answer produced by the graph.",
			Schema:      map[string]any{"type": "string"},
		},
	}
}
