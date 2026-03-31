package falcon

import fruntime "falcon/runtime"

const CommonStateSchemaID = fruntime.CommonStateSchemaID

type State = fruntime.State

func defaultStateFieldDefinitions() []StateFieldDefinition {
	defs := fruntime.DefaultStateFieldDefinitions()
	result := make([]StateFieldDefinition, 0, len(defs))
	for _, def := range defs {
		result = append(result, StateFieldDefinition{
			Name:        def.Name,
			Description: def.Description,
			Schema:      cloneJSONSchema(def.Schema),
		})
	}
	return result
}

func cloneJSONSchema(input map[string]any) JSONSchema {
	if len(input) == 0 {
		return nil
	}
	cloned := make(JSONSchema, len(input))
	for key, value := range input {
		cloned[key] = cloneJSONSchemaValue(value)
	}
	return cloned
}

func cloneJSONSchemaValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return map[string]any(cloneJSONSchema(typed))
	case []any:
		cloned := make([]any, len(typed))
		for i, item := range typed {
			cloned[i] = cloneJSONSchemaValue(item)
		}
		return cloned
	default:
		return value
	}
}
