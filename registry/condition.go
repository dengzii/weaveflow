package registry

import (
	"weaveflow/core"
	"weaveflow/dsl"
	wfstate "weaveflow/state"
)

type EdgeConditionMatcher = core.EdgeConditionMatcher[wfstate.State]

type EdgeCondition = core.EdgeCondition[wfstate.State]

func NewEdgeCondition(spec dsl.GraphConditionSpec, match EdgeConditionMatcher) EdgeCondition {
	return core.NewEdgeCondition(spec, match)
}

func CloneMap(input map[string]any) map[string]any {
	return core.CloneConditionConfig(input)
}
