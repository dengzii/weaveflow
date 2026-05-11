package registry

import "weaveflow/dsl"

type GraphResolver func(graphRef string) (dsl.GraphDefinition, error)

type ConditionDefinition struct {
	dsl.ConditionSchema
	Resolve func(dsl.GraphConditionSpec) (EdgeCondition, error) `json:"-"`
}
