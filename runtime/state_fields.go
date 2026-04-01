package runtime

type StateFieldDefinition struct {
	Name        string
	Description string
	Schema      map[string]any
}

func DefaultStateFieldDefinitions() []StateFieldDefinition {
	result := []StateFieldDefinition{}
	for _, extension := range defaultStateExtensions() {
		result = append(result, extension.FieldDefinitions()...)
	}
	return result
}
