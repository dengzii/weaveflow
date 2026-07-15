package builtin

import (
	"fmt"

	"github.com/dengzii/weaveflow/registry"
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

	if err := RegisterModules(registry); err != nil {
		return err
	}
	if err := RegisterCoreNodeTypes(registry); err != nil {
		return err
	}
	return nil
}

func RegisterModules(registry *registry.Registry) error {
	if registry == nil {
		return fmt.Errorf("registry is nil")
	}

	return registerConversationModule(registry)
}
