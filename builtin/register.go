// Package builtin wires WeaveFlow's default registry, protocols, and edge conditions.
package builtin

import (
	"fmt"

	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
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
	for identifier, reducer := range map[string]state.Reducer{
		"sum.v1":      state.SumReducer{},
		"max.v1":      state.MaxReducer{},
		"messages.v1": state.MessagesReducer{},
	} {
		if err := targetRegistry.RegisterReducer(identifier, reducer); err != nil {
			return err
		}
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
