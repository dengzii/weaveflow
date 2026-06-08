package registry

import (
	"weaveflow/core"
	"weaveflow/dsl"
	"weaveflow/state"
)

type EdgeConditionMatcher = core.EdgeConditionMatcher[*state.State]

type EdgeCondition = core.EdgeCondition[*state.State]

func NewEdgeCondition(spec dsl.GraphConditionSpec, match EdgeConditionMatcher) EdgeCondition {
	return core.NewEdgeCondition(spec, match)
}

func CloneMap(input map[string]any) map[string]any {
	return core.CloneConditionConfig(input)
}
