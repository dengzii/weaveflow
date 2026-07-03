package builtin

import (
	"context"
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
	"github.com/dengzii/weaveflow/state/accessors"

	"github.com/tmc/langchaingo/llms"
)

const (
	ConditionTypeLastMessageHasToolCalls = "last_message_has_tool_calls"
	ConditionTypeHasFinalAnswer          = "has_final_answer"
	ConditionTypeExpressionConditions    = "expression_conditions"
)

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

func registerCoreConditions(r *registry.Registry) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}

	for _, def := range []registry.ConditionDefinition{
		lastMessageHasToolCallsConditionDefinition(),
		hasFinalAnswerConditionDefinition(),
		expressionConditionsConditionDefinition(),
	} {
		if err := r.RegisterCondition(def); err != nil {
			return fmt.Errorf("register condition %q: %w", def.Type, err)
		}
	}
	return nil
}

func lastMessageHasToolCallsConditionDefinition() registry.ConditionDefinition {
	return registry.ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type:        ConditionTypeLastMessageHasToolCalls,
			Title:       "Last Message Has Tool Calls",
			Description: "Routes when the last AI message includes tool calls.",
			ConfigSchema: dsl.JSONSchema{
				"type":                 "object",
				"properties":           dsl.JSONSchema{"state_scope": dsl.JSONSchema{"type": "string"}},
				"additionalProperties": false,
			},
		},
		Resolve: func(spec dsl.GraphConditionSpec) (registry.EdgeCondition, error) {
			return LastMessageHasToolCalls(conditionStateScope(spec.Config)), nil
		},
	}
}

func hasFinalAnswerConditionDefinition() registry.ConditionDefinition {
	return registry.ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type:        ConditionTypeHasFinalAnswer,
			Title:       "Has Final Answer",
			Description: "Routes when the current state already contains a final answer.",
			ConfigSchema: dsl.JSONSchema{
				"type":                 "object",
				"properties":           dsl.JSONSchema{"state_scope": dsl.JSONSchema{"type": "string"}},
				"additionalProperties": false,
			},
		},
		Resolve: func(spec dsl.GraphConditionSpec) (registry.EdgeCondition, error) {
			return HasFinalAnswer(conditionStateScope(spec.Config)), nil
		},
	}
}

func expressionConditionsConditionDefinition() registry.ConditionDefinition {
	return registry.ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type:        ConditionTypeExpressionConditions,
			Title:       "Expression Conditions",
			Description: "Routes by evaluating serializable expressions against the current state.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"state_scope": dsl.JSONSchema{"type": "string"},
					"match":       dsl.JSONSchema{"type": "string", "enum": []string{ExpressionMatchAll, ExpressionMatchAny}},
					"expressions": dsl.JSONSchema{
						"type": "array",
						"items": dsl.JSONSchema{
							"type": "object",
							"properties": dsl.JSONSchema{
								"value1": dsl.JSONSchema{"type": "string"},
								"op": dsl.JSONSchema{"type": "string", "enum": []string{
									OperationEqual,
									OperationNotEqual,
									OperationContains,
									OperationNotContain,
								}},
								"value2": dsl.JSONSchema{"type": "string"},
								"logic": dsl.JSONSchema{"type": "string", "enum": []string{
									LogicAnd,
									LogicOr,
									LogicNot,
								}},
								"children": dsl.JSONSchema{
									"type":  "array",
									"items": dsl.JSONSchema{"type": "object"},
								},
							},
							"additionalProperties": false,
						},
					},
				},
				"required":             []string{"expressions"},
				"additionalProperties": false,
			},
		},
		Resolve: func(spec dsl.GraphConditionSpec) (registry.EdgeCondition, error) {
			cfg, err := ParseExpressionConditionConfig(spec.Config)
			if err != nil {
				return registry.EdgeCondition{}, fmt.Errorf("resolve expression condition: %w", err)
			}
			return ExpressionConditions(cfg)
		},
	}
}

func LastMessageHasToolCalls(scopes ...string) registry.EdgeCondition {
	scope := defaultConditionScope(scopes...)
	spec := dsl.GraphConditionSpec{Type: ConditionTypeLastMessageHasToolCalls}
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
	spec := dsl.GraphConditionSpec{Type: ConditionTypeHasFinalAnswer}
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

func conditionStateScope(configMap map[string]any) string {
	if _, ok := configMap["state_scope"]; ok {
		return config.String(configMap, "state_scope")
	}
	return node.DefaultScope
}

func defaultConditionScope(scopes ...string) string {
	if len(scopes) == 0 {
		return node.DefaultScope
	}
	return strings.TrimSpace(scopes[0])
}

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
		Type:   ConditionTypeExpressionConditions,
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

func ParseExpressionConditionConfig(configMap map[string]any) (ExpressionConditionConfig, error) {
	parsed := ExpressionConditionConfig{
		StateScope: config.String(configMap, "state_scope"),
		Match:      config.String(configMap, "match"),
	}

	expressions, err := parseExpressionsConfig(configMap["expressions"])
	if err != nil {
		return ExpressionConditionConfig{}, err
	}
	parsed.Expressions = expressions
	parsed = normalizeExpressionConditionConfig(parsed)
	return parsed, parsed.Validate()
}

func (c ExpressionConditionConfig) Validate() error {
	cfg := normalizeExpressionConditionConfig(c)
	if len(cfg.Expressions) == 0 {
		return fmt.Errorf("expression condition requires at least one expression")
	}
	switch cfg.Match {
	case ExpressionMatchAll, ExpressionMatchAny:
	default:
		return fmt.Errorf("expression condition match %q is invalid", cfg.Match)
	}
	for i, expression := range cfg.Expressions {
		if err := expression.Validate(); err != nil {
			return fmt.Errorf("expression %d: %w", i, err)
		}
	}
	return nil
}

func (c ExpressionConditionConfig) Map() map[string]any {
	cfg := normalizeExpressionConditionConfig(c)
	out := map[string]any{"match": cfg.Match}
	if cfg.StateScope != "" {
		out["state_scope"] = cfg.StateScope
	}
	expressions := make([]any, 0, len(cfg.Expressions))
	for _, expression := range cfg.Expressions {
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
			Value1: config.String(typed, "value1"),
			Op:     config.String(typed, "op"),
			Value2: config.String(typed, "value2"),
			Logic:  config.String(typed, "logic"),
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
	reg := state.NewRegistry()
	if err := accessors.InstallDefaultAccessors(reg); err != nil {
		return nil, err
	}
	return state.UseAccessor(state.NewAccess(reg, currentState).WithScope(scope), accessors.ConversationID)
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
