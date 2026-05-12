package state

import "weaveflow/dsl"

func DefaultStateFieldDefinitions() []dsl.StateFieldDefinition {
	result := []dsl.StateFieldDefinition{}
	for _, extension := range defaultStateExtensions() {
		result = append(result, extension.FieldDefinitions()...)
	}
	return result
}
