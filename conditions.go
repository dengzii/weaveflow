package weaveflow

import (
	"weaveflow/builtin"
	"weaveflow/registry"
)

type EdgeConditionMatcher = registry.EdgeConditionMatcher
type EdgeCondition = registry.EdgeCondition

const (
	OperationEqual      = builtin.OperationEqual
	OperationNotEqual   = builtin.OperationNotEqual
	OperationContains   = builtin.OperationContains
	OperationNotContain = builtin.OperationNotContain
)

const (
	ExpressionMatchAll = builtin.ExpressionMatchAll
	ExpressionMatchAny = builtin.ExpressionMatchAny
)

type Expression = builtin.Expression
type ExpressionConditionConfig = builtin.ExpressionConditionConfig

func NewEdgeCondition(spec GraphConditionSpec, match EdgeConditionMatcher) EdgeCondition {
	return registry.NewEdgeCondition(spec, match)
}

func LastMessageHasToolCalls(scope string) EdgeCondition {
	return builtin.LastMessageHasToolCalls(scope)
}

func HasFinalAnswer(scope string) EdgeCondition {
	return builtin.HasFinalAnswer(scope)
}

func ExpressionConditions(config ExpressionConditionConfig) (EdgeCondition, error) {
	return builtin.ExpressionConditions(config)
}

func ParseExpressionConditionConfig(config map[string]any) (ExpressionConditionConfig, error) {
	return builtin.ParseExpressionConditionConfig(config)
}
