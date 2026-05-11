package registry

import (
	"context"
	"fmt"

	"weaveflow/dsl"
	fruntime "weaveflow/runtime"
)

type EdgeConditionMatcher func(ctx context.Context, state fruntime.State) bool

type EdgeCondition struct {
	Spec  dsl.GraphConditionSpec
	Match EdgeConditionMatcher
}

func NewEdgeCondition(spec dsl.GraphConditionSpec, match EdgeConditionMatcher) EdgeCondition {
	return EdgeCondition{
		Spec:  dsl.NormalizeGraphConditionSpec(spec),
		Match: match,
	}
}

func (c EdgeCondition) Validate() error {
	spec := dsl.NormalizeGraphConditionSpec(c.Spec)
	if spec.Type == "" {
		return fmt.Errorf("condition spec type is required")
	}
	if c.Match == nil {
		return fmt.Errorf("condition matcher is nil")
	}
	return nil
}

func (c EdgeCondition) WithSpec(spec dsl.GraphConditionSpec) EdgeCondition {
	c.Spec = dsl.NormalizeGraphConditionSpec(spec)
	return c
}

func (c EdgeCondition) CloneSpec() dsl.GraphConditionSpec {
	spec := dsl.NormalizeGraphConditionSpec(c.Spec)
	if len(spec.Config) > 0 {
		spec.Config = cloneMap(spec.Config)
	}
	return spec
}

func cloneMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = cloneValue(value)
	}
	return cloned
}

func cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneMap(typed)
	case []any:
		cloned := make([]any, len(typed))
		for i, item := range typed {
			cloned[i] = cloneValue(item)
		}
		return cloned
	case []string:
		cloned := make([]string, len(typed))
		copy(cloned, typed)
		return cloned
	default:
		return value
	}
}
