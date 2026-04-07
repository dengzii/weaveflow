package falcon

import (
	"context"
	"falcon/dsl"
	fruntime "falcon/runtime"
	"fmt"
	"strconv"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

type EdgeConditionMatcher func(ctx context.Context, state State) bool

// EdgeCondition is a condition that can be applied to an edge in a graph.
type EdgeCondition struct {
	Spec  GraphConditionSpec
	Match EdgeConditionMatcher
}

func NewEdgeCondition(spec GraphConditionSpec, match EdgeConditionMatcher) EdgeCondition {
	return EdgeCondition{
		Spec:  dsl.NormalizeGraphConditionSpec(spec),
		Match: match,
	}
}

func (c EdgeCondition) validate() error {
	spec := dsl.NormalizeGraphConditionSpec(c.Spec)
	if spec.Type == "" {
		return fmt.Errorf("condition spec type is required")
	}
	if c.Match == nil {
		return fmt.Errorf("condition matcher is nil")
	}
	return nil
}

func (c EdgeCondition) withSpec(spec GraphConditionSpec) EdgeCondition {
	c.Spec = dsl.NormalizeGraphConditionSpec(spec)
	return c
}

func (c EdgeCondition) cloneSpec() GraphConditionSpec {
	spec := dsl.NormalizeGraphConditionSpec(c.Spec)
	if len(spec.Config) > 0 {
		spec.Config = cloneMap(spec.Config)
	}
	return spec
}

func LastMessageHasToolCalls(scope string) EdgeCondition {
	scope = strings.TrimSpace(scope)
	spec := GraphConditionSpec{Type: "last_message_has_tool_calls"}
	if scope != "" {
		spec.Config = map[string]any{
			"state_scope": scope,
		}
	}
	return NewEdgeCondition(spec, func(_ context.Context, state State) bool {
		messages := fruntime.Conversation(state, scope).Messages()
		if len(messages) == 0 {
			return false
		}

		lastMessage := messages[len(messages)-1]
		if lastMessage.Role != llms.ChatMessageTypeAI {
			return false
		}

		for _, part := range lastMessage.Parts {
			if _, ok := part.(llms.ToolCall); ok {
				return true
			}
		}

		return false
	})
}

func HasFinalAnswer(scope string) EdgeCondition {
	scope = strings.TrimSpace(scope)
	spec := GraphConditionSpec{Type: "has_final_answer"}
	if scope != "" {
		spec.Config = map[string]any{
			"state_scope": scope,
		}
	}
	return NewEdgeCondition(spec, func(_ context.Context, state State) bool {
		return fruntime.Conversation(state, scope).FinalAnswer() != ""
	})
}

const (
	OperationEqual      = "equals"
	OperationNotEqual   = "not_equals"
	OperationContains   = "contains"
	OperationNotContain = "not_contains"
)

const (
	ExpressionMatchAll = "all"
	ExpressionMatchAny = "any"
)

type Expression struct {
	Value1 string `json:"value1"`
	Op     string `json:"op"`
	Value2 string `json:"value2"`
}

type ExpressionConditionConfig struct {
	StateScope  string       `json:"state_scope,omitempty"`
	Match       string       `json:"match,omitempty"`
	Expressions []Expression `json:"expressions"`
}

func ExpressionConditions(config ExpressionConditionConfig) (EdgeCondition, error) {
	config = normalizeExpressionConditionConfig(config)
	if err := config.Validate(); err != nil {
		return EdgeCondition{}, err
	}

	expressions := append([]Expression(nil), config.Expressions...)
	matchMode := config.Match
	scope := config.StateScope

	return NewEdgeCondition(GraphConditionSpec{
		Type:   "expression_conditions",
		Config: config.Map(),
	}, func(_ context.Context, state State) bool {
		switch matchMode {
		case ExpressionMatchAny:
			for _, expression := range expressions {
				if matchExpression(state, scope, expression) {
					return true
				}
			}
			return false
		default:
			for _, expression := range expressions {
				if !matchExpression(state, scope, expression) {
					return false
				}
			}
			return true
		}
	}), nil
}

func ParseExpressionConditionConfig(config map[string]any) (ExpressionConditionConfig, error) {
	parsed := ExpressionConditionConfig{
		StateScope: stringConfig(config, "state_scope"),
		Match:      stringConfig(config, "match"),
	}

	expressions, err := parseExpressionsConfig(config["expressions"])
	if err != nil {
		return ExpressionConditionConfig{}, err
	}
	parsed.Expressions = expressions
	parsed = normalizeExpressionConditionConfig(parsed)
	return parsed, parsed.Validate()
}

func (c ExpressionConditionConfig) Validate() error {
	config := normalizeExpressionConditionConfig(c)
	if len(config.Expressions) == 0 {
		return fmt.Errorf("expression condition requires at least one expression")
	}
	switch config.Match {
	case ExpressionMatchAll, ExpressionMatchAny:
	default:
		return fmt.Errorf("expression condition match %q is invalid", config.Match)
	}
	for i, expression := range config.Expressions {
		if err := expression.Validate(); err != nil {
			return fmt.Errorf("expression %d: %w", i, err)
		}
	}
	return nil
}

func (c ExpressionConditionConfig) Map() map[string]any {
	config := normalizeExpressionConditionConfig(c)
	out := map[string]any{
		"match": config.Match,
	}
	if config.StateScope != "" {
		out["state_scope"] = config.StateScope
	}
	expressions := make([]any, 0, len(config.Expressions))
	for _, expression := range config.Expressions {
		expression = normalizeExpression(expression)
		expressions = append(expressions, map[string]any{
			"value1": expression.Value1,
			"op":     expression.Op,
			"value2": expression.Value2,
		})
	}
	out["expressions"] = expressions
	return out
}

func (e Expression) Validate() error {
	expression := normalizeExpression(e)
	if expression.Value1 == "" {
		return fmt.Errorf("expression value1 is required")
	}
	switch expression.Op {
	case OperationEqual, OperationNotEqual, OperationContains, OperationNotContain:
		return nil
	default:
		return fmt.Errorf("expression op %q is invalid", expression.Op)
	}
}

func normalizeExpressionConditionConfig(config ExpressionConditionConfig) ExpressionConditionConfig {
	config.StateScope = strings.TrimSpace(config.StateScope)
	config.Match = strings.ToLower(strings.TrimSpace(config.Match))
	if config.Match == "" {
		config.Match = ExpressionMatchAll
	}
	if len(config.Expressions) == 0 {
		config.Expressions = nil
		return config
	}
	normalized := make([]Expression, 0, len(config.Expressions))
	for _, expression := range config.Expressions {
		normalized = append(normalized, normalizeExpression(expression))
	}
	config.Expressions = normalized
	return config
}

func normalizeExpression(expression Expression) Expression {
	expression.Value1 = strings.TrimSpace(expression.Value1)
	expression.Op = strings.ToLower(strings.TrimSpace(expression.Op))
	expression.Value2 = strings.TrimSpace(expression.Value2)
	return expression
}

func parseExpressionsConfig(raw any) ([]Expression, error) {
	switch typed := raw.(type) {
	case nil:
		return nil, nil
	case []Expression:
		result := make([]Expression, 0, len(typed))
		for _, expression := range typed {
			result = append(result, normalizeExpression(expression))
		}
		return result, nil
	case []map[string]any:
		result := make([]Expression, 0, len(typed))
		for i, item := range typed {
			expression, err := parseExpression(item)
			if err != nil {
				return nil, fmt.Errorf("parse expressions[%d]: %w", i, err)
			}
			result = append(result, expression)
		}
		return result, nil
	case []any:
		result := make([]Expression, 0, len(typed))
		for i, item := range typed {
			expression, err := parseExpression(item)
			if err != nil {
				return nil, fmt.Errorf("parse expressions[%d]: %w", i, err)
			}
			result = append(result, expression)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("expression condition expressions must be an array")
	}
}

func parseExpression(raw any) (Expression, error) {
	switch typed := raw.(type) {
	case Expression:
		expression := normalizeExpression(typed)
		return expression, expression.Validate()
	case map[string]any:
		expression := normalizeExpression(Expression{
			Value1: stringConfig(typed, "value1"),
			Op:     stringConfig(typed, "op"),
			Value2: stringConfig(typed, "value2"),
		})
		return expression, expression.Validate()
	default:
		return Expression{}, fmt.Errorf("expression item must be an object")
	}
}

func matchExpression(state State, scope string, expression Expression) bool {
	expression = normalizeExpression(expression)
	left, ok := resolveExpressionValue(state, scope, expression.Value1)
	switch expression.Op {
	case OperationEqual:
		return ok && expressionValueEquals(left, expression.Value2)
	case OperationNotEqual:
		return !ok || !expressionValueEquals(left, expression.Value2)
	case OperationContains:
		return ok && expressionValueContains(left, expression.Value2)
	case OperationNotContain:
		return !ok || !expressionValueContains(left, expression.Value2)
	default:
		return false
	}
}

func resolveExpressionValue(state State, scope, path string) (any, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, false
	}

	segments := strings.Split(path, ".")
	for i := range segments {
		segments[i] = strings.TrimSpace(segments[i])
		if segments[i] == "" {
			return nil, false
		}
	}

	if isConversationField(segments[0]) {
		value, ok := conversationFieldValue(state, scope, segments[0])
		if !ok {
			return nil, false
		}
		return traverseExpressionValue(value, segments[1:])
	}

	var base any = state
	if scope != "" {
		base = state.Scope(scope)
	}
	return traverseExpressionValue(base, segments)
}

func isConversationField(field string) bool {
	switch field {
	case fruntime.StateKeyMessages, fruntime.StateKeyIterationCount, fruntime.StateKeyMaxIterations, fruntime.StateKeyFinalAnswer:
		return true
	default:
		return false
	}
}

func conversationFieldValue(state State, scope, field string) (any, bool) {
	conversation := fruntime.Conversation(state, scope)
	switch field {
	case fruntime.StateKeyMessages:
		return conversation.Messages(), true
	case fruntime.StateKeyIterationCount:
		return conversation.IterationCount(), true
	case fruntime.StateKeyMaxIterations:
		return conversation.MaxIterations(), true
	case fruntime.StateKeyFinalAnswer:
		return conversation.FinalAnswer(), true
	default:
		return nil, false
	}
}

func traverseExpressionValue(current any, segments []string) (any, bool) {
	value := current
	for _, segment := range segments {
		next, ok := expressionPathSegment(value, segment)
		if !ok {
			return nil, false
		}
		value = next
	}
	return value, true
}

func expressionPathSegment(current any, segment string) (any, bool) {
	switch typed := current.(type) {
	case nil:
		return nil, false
	case State:
		next, ok := typed[segment]
		return next, ok
	case map[string]any:
		next, ok := typed[segment]
		return next, ok
	case []any:
		index, ok := expressionIndex(segment, len(typed))
		if !ok {
			return nil, false
		}
		return typed[index], true
	case []map[string]any:
		index, ok := expressionIndex(segment, len(typed))
		if !ok {
			return nil, false
		}
		return typed[index], true
	case []string:
		index, ok := expressionIndex(segment, len(typed))
		if !ok {
			return nil, false
		}
		return typed[index], true
	default:
		return nil, false
	}
}

func expressionIndex(segment string, size int) (int, bool) {
	index, err := strconv.Atoi(segment)
	if err != nil || index < 0 || index >= size {
		return 0, false
	}
	return index, true
}

func expressionValueEquals(left any, right string) bool {
	return strings.TrimSpace(expressionValueText(left)) == strings.TrimSpace(right)
}

func expressionValueContains(left any, right string) bool {
	right = strings.TrimSpace(right)
	switch typed := left.(type) {
	case []string:
		for _, item := range typed {
			if strings.TrimSpace(item) == right {
				return true
			}
		}
		return false
	case []any:
		for _, item := range typed {
			if strings.TrimSpace(expressionValueText(item)) == right {
				return true
			}
		}
		return false
	case []map[string]any:
		for _, item := range typed {
			if strings.TrimSpace(expressionValueText(item)) == right {
				return true
			}
		}
		return false
	default:
		return strings.Contains(expressionValueText(left), right)
	}
}

func expressionValueText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}
