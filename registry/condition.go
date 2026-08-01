package registry

import (
	"context"
	"fmt"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/state"
)

type EdgeConditionMatcher func(context.Context, *state.State) bool

type EdgeCondition struct {
	Spec  dsl.GraphConditionSpec
	Match EdgeConditionMatcher
}

func NewEdgeCondition(spec dsl.GraphConditionSpec, match EdgeConditionMatcher) EdgeCondition {
	return EdgeCondition{Spec: dsl.NormalizeGraphConditionSpec(spec), Match: match}
}

func (condition EdgeCondition) Validate() error {
	spec := dsl.NormalizeGraphConditionSpec(condition.Spec)
	if spec.Type == "" {
		return fmt.Errorf("condition spec type is required")
	}
	if condition.Match == nil {
		return fmt.Errorf("condition matcher is nil")
	}
	return nil
}

func (condition EdgeCondition) WithSpec(spec dsl.GraphConditionSpec) EdgeCondition {
	condition.Spec = dsl.NormalizeGraphConditionSpec(spec)
	return condition
}

func (condition EdgeCondition) CloneSpec() dsl.GraphConditionSpec {
	return dsl.CloneGraphConditionSpec(condition.Spec)
}
