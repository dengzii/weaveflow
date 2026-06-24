package builtin

import (
	"fmt"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state/accessors"
)

func NewDefaultRegistry() *registry.Registry {
	r := registry.NewRegistry()
	if err := RegisterDefaultComponents(r); err != nil {
		panic(err)
	}
	return r
}

func RegisterDefaultComponents(registry *registry.Registry) error {
	if registry == nil {
		return fmt.Errorf("registry is nil")
	}

	if err := RegisterDefaultStateFields(registry); err != nil {
		return err
	}
	if err := RegisterModules(registry); err != nil {
		return err
	}
	if err := RegisterCoreNodeTypes(registry); err != nil {
		return err
	}
	return nil
}

func RegisterDefaultStateFields(registry *registry.Registry) error {
	if registry == nil {
		return fmt.Errorf("registry is nil")
	}

	return registry.RegisterStateField(dsl.StateFieldDefinition{
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

func RegisterModules(registry *registry.Registry) error {
	if registry == nil {
		return fmt.Errorf("registry is nil")
	}

	return registerConversationModule(registry)
}
