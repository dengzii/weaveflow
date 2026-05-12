package builtin

import (
	"weaveflow/builtin/modules/conversation"
	"weaveflow/builtin/modules/execution"
	"weaveflow/builtin/modules/memory"
	"weaveflow/builtin/modules/planning"
	"weaveflow/builtin/modules/safety"
	"weaveflow/builtin/modules/verification"
	"weaveflow/registry"
	"weaveflow/state"
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

	for _, def := range state.DefaultStateFieldDefinitions() {
		registry.RegisterStateField(def)
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
