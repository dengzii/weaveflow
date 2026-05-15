package builtin

import (
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

	registerConversationModule(registry)
	registerPlanningModule(registry)
	registerMemoryModule(registry)
	registerExecutionModule(registry)
	registerVerificationModule(registry)
	registerSafetyModule(registry)
}
