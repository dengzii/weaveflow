package builtin

import (
	"context"
	"fmt"
	"strings"

	"weaveflow/dsl"
	"weaveflow/node"
	"weaveflow/registry"
	"weaveflow/state"
	"weaveflow/state/accessors"

	"github.com/tmc/langchaingo/llms"
)

func LastMessageHasToolCalls(scopes ...string) registry.EdgeCondition {
	scope := defaultConditionScope(scopes...)
	spec := dsl.GraphConditionSpec{Type: "last_message_has_tool_calls"}
	if scope != "" {
		spec.Config = map[string]any{"state_scope": scope}
	}
	return registry.NewEdgeCondition(spec, func(_ context.Context, state *state.State) bool {
		conversation, err := conversationForCondition(state, scope)
		if err != nil {
			return false
		}
		messages := conversation.Messages()
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

func HasFinalAnswer(scopes ...string) registry.EdgeCondition {
	scope := defaultConditionScope(scopes...)
	spec := dsl.GraphConditionSpec{Type: "has_final_answer"}
	if scope != "" {
		spec.Config = map[string]any{"state_scope": scope}
	}
	return registry.NewEdgeCondition(spec, func(_ context.Context, state *state.State) bool {
		conversation, err := conversationForCondition(state, scope)
		if err != nil {
			return false
		}
		return conversation.FinalAnswer() != ""
	})
}

func defaultConditionScope(scopes ...string) string {
	if len(scopes) == 0 {
		return node.DefaultScope
	}
	return strings.TrimSpace(scopes[0])
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

const (
	LogicAnd = "and"
	LogicOr  = "or"
	LogicNot = "not"
)

type Expression struct {
	Value1 string `json:"value1,omitempty"`
	Op     string `json:"op,omitempty"`
	Value2 string `json:"value2,omitempty"`

	Logic    string       `json:"logic,omitempty"`
	Children []Expression `json:"children,omitempty"`
}

func (e Expression) IsComposite() bool {
	return strings.TrimSpace(e.Logic) != ""
}

type ExpressionConditionConfig struct {
	StateScope  string       `json:"state_scope,omitempty"`
	Match       string       `json:"match,omitempty"`
	Expressions []Expression `json:"expressions"`
}

func ExpressionConditions(config ExpressionConditionConfig) (registry.EdgeCondition, error) {
	config = normalizeExpressionConditionConfig(config)
	if err := config.Validate(); err != nil {
		return registry.EdgeCondition{}, err
	}

	expressions := append([]Expression(nil), config.Expressions...)
	matchMode := config.Match
	scope := config.StateScope

	return registry.NewEdgeCondition(dsl.GraphConditionSpec{
		Type:   "expression_conditions",
		Config: config.Map(),
	}, func(_ context.Context, state *state.State) bool {
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
		StateScope: registry.StringConfig(config, "state_scope"),
		Match:      registry.StringConfig(config, "match"),
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
	out := map[string]any{"match": config.Match}
	if config.StateScope != "" {
		out["state_scope"] = config.StateScope
	}
	expressions := make([]any, 0, len(config.Expressions))
	for _, expression := range config.Expressions {
		expressions = append(expressions, expression.Map())
	}
	out["expressions"] = expressions
	return out
}

func (e Expression) Validate() error {
	expression := normalizeExpression(e)
	if expression.Logic != "" {
		switch expression.Logic {
		case LogicAnd, LogicOr, LogicNot:
		default:
			return fmt.Errorf("expression logic %q is invalid", expression.Logic)
		}
		if expression.Value1 != "" || expression.Op != "" || expression.Value2 != "" {
			return fmt.Errorf("composite expression must not set value1/op/value2")
		}
		if len(expression.Children) == 0 {
			return fmt.Errorf("composite expression %q requires at least one child", expression.Logic)
		}
		if expression.Logic == LogicNot && len(expression.Children) != 1 {
			return fmt.Errorf("composite expression %q requires exactly one child", expression.Logic)
		}
		for i, child := range expression.Children {
			if err := child.Validate(); err != nil {
				return fmt.Errorf("child %d: %w", i, err)
			}
		}
		return nil
	}
	if len(expression.Children) > 0 {
		return fmt.Errorf("leaf expression must not set children")
	}
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

func (e Expression) Map() map[string]any {
	expression := normalizeExpression(e)
	if expression.Logic != "" {
		children := make([]any, 0, len(expression.Children))
		for _, child := range expression.Children {
			children = append(children, child.Map())
		}
		return map[string]any{
			"logic":    expression.Logic,
			"children": children,
		}
	}
	return map[string]any{
		"value1": expression.Value1,
		"op":     expression.Op,
		"value2": expression.Value2,
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
	expression.Logic = strings.ToLower(strings.TrimSpace(expression.Logic))
	if len(expression.Children) > 0 {
		normalized := make([]Expression, 0, len(expression.Children))
		for _, child := range expression.Children {
			normalized = append(normalized, normalizeExpression(child))
		}
		expression.Children = normalized
	} else {
		expression.Children = nil
	}
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
		expression := Expression{
			Value1: registry.StringConfig(typed, "value1"),
			Op:     registry.StringConfig(typed, "op"),
			Value2: registry.StringConfig(typed, "value2"),
			Logic:  registry.StringConfig(typed, "logic"),
		}
		if rawChildren, ok := typed["children"]; ok && rawChildren != nil {
			children, err := parseExpressionsConfig(rawChildren)
			if err != nil {
				return Expression{}, err
			}
			expression.Children = children
		}
		expression = normalizeExpression(expression)
		return expression, expression.Validate()
	default:
		return Expression{}, fmt.Errorf("expression item must be an object")
	}
}

func matchExpression(state *state.State, scope string, expression Expression) bool {
	expression = normalizeExpression(expression)
	if expression.Logic != "" {
		switch expression.Logic {
		case LogicAnd:
			for _, child := range expression.Children {
				if !matchExpression(state, scope, child) {
					return false
				}
			}
			return true
		case LogicOr:
			for _, child := range expression.Children {
				if matchExpression(state, scope, child) {
					return true
				}
			}
			return false
		case LogicNot:
			if len(expression.Children) != 1 {
				return false
			}
			return !matchExpression(state, scope, expression.Children[0])
		default:
			return false
		}
	}
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

func resolveExpressionValue(currentState *state.State, scope, path string) (any, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, false
	}
	if isExplicitContractStatePath(path) {
		return state.ReadPath(currentState, path)
	}

	segments := state.SplitStatePath(path)
	if len(segments) == 0 {
		return nil, false
	}

	if isConversationField(segments[0]) {
		value, ok := conversationFieldValue(currentState, scope, segments[0])
		if !ok {
			return nil, false
		}
		if len(segments) == 1 {
			return value, true
		}
		return state.ResolveStateValue(value, segments[1:])
	}

	var readPath state.Path
	if scope != "" {
		readPath = state.Scope(scope, segments...)
	} else {
		readPath = state.Shared(segments...)
	}
	return state.NewAccess(nil, currentState).ReadAny(readPath)
}

func isExplicitContractStatePath(path string) bool {
	switch {
	case path == "shared" || strings.HasPrefix(path, "shared."):
		return true
	case path == "scopes" || strings.HasPrefix(path, "scopes."):
		return true
	case path == "runtime" || strings.HasPrefix(path, "runtime."):
		return true
	case path == "internal" || strings.HasPrefix(path, "internal."):
		return true
	default:
		return false
	}
}

func isConversationField(field string) bool {
	switch field {
	case accessors.ConversationFieldMessages, accessors.ConversationFieldIterationCount, accessors.ConversationFieldMaxIterations, accessors.ConversationFieldFinalAnswer:
		return true
	default:
		return false
	}
}

func conversationFieldValue(currentState *state.State, scope, field string) (any, bool) {
	conversation, err := conversationForCondition(currentState, scope)
	if err != nil {
		return nil, false
	}
	switch field {
	case accessors.ConversationFieldMessages:
		return conversation.Messages(), true
	case accessors.ConversationFieldIterationCount:
		return conversation.IterationCount(), true
	case accessors.ConversationFieldMaxIterations:
		return conversation.MaxIterations(), true
	case accessors.ConversationFieldFinalAnswer:
		return conversation.FinalAnswer(), true
	default:
		return nil, false
	}
}

func conversationForCondition(currentState *state.State, scope string) (accessors.Conversation, error) {
	registry := state.NewRegistry()
	if err := accessors.InstallDefaultAccessors(registry); err != nil {
		return nil, err
	}
	return state.UseAccessor(state.NewAccess(registry, currentState).WithScope(scope), accessors.ConversationID)
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
