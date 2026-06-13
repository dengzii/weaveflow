package builtin

import (
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state/accessors"
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

	registry.RegisterStateField(dsl.StateFieldDefinition{
		Name:        accessors.KeyConversation,
		Description: "Shared conversation state for the graph run.",
		Schema: dsl.JSONSchema{
			"type": "object",
			"properties": dsl.JSONSchema{
				accessors.ConversationFieldMessages:       dsl.JSONSchema{"type": "array", "items": dsl.JSONSchema{"type": "object"}},
				accessors.ConversationFieldIterationCount: dsl.JSONSchema{"type": "integer", "minimum": 0},
				accessors.ConversationFieldMaxIterations:  dsl.JSONSchema{"type": "integer", "minimum": 1},
				accessors.ConversationFieldFinalAnswer:    dsl.JSONSchema{"type": "string"},
			},
			"additionalProperties": true,
		},
	})
}

func RegisterModules(registry *registry.Registry) {
	if registry == nil {
		return
	}

	registerConversationModule(registry)
}
