package registry

import (
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/state"
)

type EdgeConditionMatcher = core.EdgeConditionMatcher[*state.State]

type EdgeCondition = core.EdgeCondition[*state.State]

func NewEdgeCondition(spec dsl.GraphConditionSpec, match EdgeConditionMatcher) EdgeCondition {
	return core.NewEdgeCondition(spec, match)
}
