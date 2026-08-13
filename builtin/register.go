// Package builtin wires WeaveFlow's default registry, protocols, and edge conditions.
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

func RegisterDefaultComponents(targetRegistry *registry.Registry) error {
	if targetRegistry == nil {
		return fmt.Errorf("registry is nil")
	}

	if err := RegisterModules(targetRegistry); err != nil {
		return err
	}
	if err := RegisterCoreNodeTypes(targetRegistry); err != nil {
		return err
	}
	return nil
}

func RegisterModules(targetRegistry *registry.Registry) error {
	if targetRegistry == nil {
		return fmt.Errorf("registry is nil")
	}

	return registerConversationModule(targetRegistry)
}
