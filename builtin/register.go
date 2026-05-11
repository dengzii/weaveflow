package builtin

import (
	"weaveflow/builtin/modules/conversation"
	"weaveflow/builtin/modules/execution"
	"weaveflow/builtin/modules/memory"
	"weaveflow/builtin/modules/planning"
	"weaveflow/builtin/modules/safety"
	"weaveflow/builtin/modules/verification"
	"weaveflow/dsl"
	"weaveflow/registry"
	"weaveflow/runtime"
)

func NewDefaultRegistry() *registry.Registry {
	r := registry.NewRegistry()
	RegisterDefaultComponents(r)
	return r
}

func RegisterDefaultComponents(registry *registry.Registry) {
	if registry == nil {
		return
	}

	RegisterDefaultStateFields(registry)
	RegisterModules(registry)
	RegisterCoreNodeTypes(registry)
}

func RegisterDefaultStateFields(registry *registry.Registry) {
	if registry == nil {
		return
	}

	for _, def := range runtime.DefaultStateFieldDefinitions() {
		registry.RegisterStateField(dsl.StateFieldDefinition{
			Name:        def.Name,
			Description: def.Description,
			Schema:      cloneJSONSchema(def.Schema),
		})
	}
}

func RegisterModules(registry *registry.Registry) {
	if registry == nil {
		return
	}

	conversation.Register(registry)
	planning.Register(registry)
	memory.Register(registry)
	execution.Register(registry)
	verification.Register(registry)
	safety.Register(registry)
}

func cloneJSONSchema(input map[string]any) dsl.JSONSchema {
	if len(input) == 0 {
		return nil
	}
	cloned := make(dsl.JSONSchema, len(input))
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
