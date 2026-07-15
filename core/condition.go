package core

import (
	"context"
	"fmt"
	"strings"
)

type GraphConditionSpec struct {
	Type   string                  `json:"type"`
	Config map[string]any          `json:"config,omitempty"`
	State  map[string]StateBinding `json:"state,omitempty"`
}

type StateBinding struct {
	Path string `json:"path"`
}

func NormalizeGraphConditionSpec(spec GraphConditionSpec) GraphConditionSpec {
	spec.Type = strings.TrimSpace(spec.Type)
	if len(spec.Config) == 0 {
		spec.Config = nil
	}
	if len(spec.State) == 0 {
		spec.State = nil
	} else {
		bindings := make(map[string]StateBinding, len(spec.State))
		for name, binding := range spec.State {
			binding.Path = strings.TrimSpace(binding.Path)
			bindings[name] = binding
		}
		spec.State = bindings
	}
	return spec
}

type EdgeConditionMatcher[S any] func(ctx context.Context, state S) bool

type EdgeCondition[S any] struct {
	Spec  GraphConditionSpec
	Match EdgeConditionMatcher[S]
}

func NewEdgeCondition[S any](spec GraphConditionSpec, match EdgeConditionMatcher[S]) EdgeCondition[S] {
	return EdgeCondition[S]{
		Spec:  NormalizeGraphConditionSpec(spec),
		Match: match,
	}
}

func (c EdgeCondition[S]) Validate() error {
	spec := NormalizeGraphConditionSpec(c.Spec)
	if spec.Type == "" {
		return fmt.Errorf("condition spec type is required")
	}
	if c.Match == nil {
		return fmt.Errorf("condition matcher is nil")
	}
	return nil
}

func (c EdgeCondition[S]) WithSpec(spec GraphConditionSpec) EdgeCondition[S] {
	c.Spec = NormalizeGraphConditionSpec(spec)
	return c
}

func (c EdgeCondition[S]) CloneSpec() GraphConditionSpec {
	spec := NormalizeGraphConditionSpec(c.Spec)
	if len(spec.Config) > 0 {
		spec.Config = CloneConditionConfig(spec.Config)
	}
	if len(spec.State) > 0 {
		bindings := spec.State
		spec.State = make(map[string]StateBinding, len(bindings))
		for key, binding := range bindings {
			spec.State[key] = binding
		}
	}
	return spec
}

func CloneConditionConfig(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = cloneConditionConfigValue(value)
	}
	return cloned
}

func cloneConditionConfigValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return CloneConditionConfig(typed)
	case []any:
		cloned := make([]any, len(typed))
		for i, item := range typed {
			cloned[i] = cloneConditionConfigValue(item)
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
