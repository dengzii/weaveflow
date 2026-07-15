package registry

import "github.com/dengzii/weaveflow/dsl"

type GraphResolver func(graphRef string) (dsl.GraphDefinition, error)

type ConditionDefinition struct {
	dsl.ConditionSchema
	StatePorts []dsl.StatePortDefinition                          `json:"state_ports"`
	Resolve    func(ResolvedConditionSpec) (EdgeCondition, error) `json:"-"`
}
