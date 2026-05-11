package weaveflow

import (
	"weaveflow/dsl"
	"weaveflow/registry"
)

func ApplyGraphInstanceConfig(def GraphDefinition, instance dsl.GraphInstanceConfig) (GraphDefinition, error) {
	return registry.ApplyGraphInstanceConfig(def, instance)
}
