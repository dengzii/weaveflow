package builtin

import (
	"context"
	"fmt"
	"sort"
	"strings"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	plancap "github.com/dengzii/weaveflow/capability/plan"
	supervisorcap "github.com/dengzii/weaveflow/capability/supervisor"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/internal/stateexpr"
	plannode "github.com/dengzii/weaveflow/node/plan"
	supervisornode "github.com/dengzii/weaveflow/node/supervisor"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"

	"github.com/dengzii/weaveflow/llms"
)

const (
	ConditionTypeConversationHasToolCalls   = "conversation_has_tool_calls"
	ConditionTypeConversationHasFinalAnswer = "conversation_has_final_answer"
	ConditionTypeExpressionConditions       = "expression_conditions"
	ConditionTypeStateExpression            = "state_expression"
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
		stateExpressionConditionDefinition(),
		planStatusEqualsConditionDefinition(),
		supervisorRouteEqualsConditionDefinition(),
	} {
		if err := r.RegisterCondition(def); err != nil {
			return fmt.Errorf("register condition %q: %w", def.Type, err)
		}
	}
	return nil
}

func stateExpressionConditionDefinition() registry.ConditionDefinition {
	return registry.ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type:        ConditionTypeStateExpression,
			Title:       "State Expression",
			Description: "Routes when a restricted CEL expression over explicitly bound state inputs returns true.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"expression": dsl.JSONSchema{
						"type": "string", "title": "CEL Expression", "x-control": "textarea",
						"description": "Boolean CEL expression. Access bound values with inputs.<alias>.",
					},
				},
				"required":             []string{"expression"},
				"additionalProperties": false,
			},
			DynamicStatePorts: &dsl.DynamicStatePortDefinition{
				Description:   "JSON value exposed to CEL as inputs.<alias>.",
				NamePattern:   stateexpr.InputAliasPattern,
				MinPorts:      1,
				Required:      false,
				Schema:        dsl.JSONSchema{"title": "Any JSON value"},
				Mode:          dsl.StateAccessRead,
				MergeStrategy: dsl.StateMergeReplace,
			},
		},
		Resolve: func(resolved registry.ResolvedConditionSpec) (registry.EdgeCondition, error) {
			for key := range resolved.Spec.Config {
				if key != "expression" {
					return registry.EdgeCondition{}, fmt.Errorf("resolve %s condition: unknown config field %q", ConditionTypeStateExpression, key)
				}
			}
			rawExpression, exists := resolved.Spec.Config["expression"]
			if !exists {
				return registry.EdgeCondition{}, fmt.Errorf("resolve %s condition: config field %q is required", ConditionTypeStateExpression, "expression")
			}
			expression, ok := rawExpression.(string)
			if !ok || strings.TrimSpace(expression) == "" {
				return registry.EdgeCondition{}, fmt.Errorf("resolve %s condition: config field %q must be a non-empty string", ConditionTypeStateExpression, "expression")
			}
			paths := make(map[string]state.Path, len(resolved.State))
			for name, binding := range resolved.State {
				if binding.Path.Empty() {
					return registry.EdgeCondition{}, fmt.Errorf("resolve %s condition: state input %q has no resolved path", ConditionTypeStateExpression, name)
				}
				paths[name] = binding.Path
			}
			condition, err := StateExpression(paths, expression)
			if err != nil {
				return registry.EdgeCondition{}, fmt.Errorf("resolve %s condition: %w", ConditionTypeStateExpression, err)
			}
			return condition, nil
		},
	}
}

func StateExpression(paths map[string]state.Path, expression string) (registry.EdgeCondition, error) {
	if len(paths) == 0 {
		return registry.EdgeCondition{}, fmt.Errorf("at least one state input is required")
	}
	program, err := stateexpr.Compile(expression, stateexpr.CompileOptions{RequireBoolean: true})
	if err != nil {
		return registry.EdgeCondition{}, err
	}
	names := make([]string, 0, len(paths))
	bindings := make(map[string]dsl.StateBinding, len(paths))
	for name, path := range paths {
		if path.Empty() {
			return registry.EdgeCondition{}, fmt.Errorf("state input %q path is required", name)
		}
		names = append(names, name)
		bindings[name] = dsl.StateBinding{Path: path.String()}
	}
	sort.Strings(names)
	return registry.NewEdgeCondition(dsl.GraphConditionSpec{
		Type: ConditionTypeStateExpression, Config: map[string]any{"expression": strings.TrimSpace(expression)}, State: bindings,
	}, func(ctx context.Context, current *state.State) (bool, error) {
		access := state.NewAccess(current)
		inputs := make(map[string]any, len(names))
		for _, name := range names {
			value, ok := access.ReadAny(paths[name])
			if !ok {
				return false, nil
			}
			inputs[name] = value
		}
		return program.EvalBool(ctx, inputs)
	}), nil
}

func supervisorRouteEqualsConditionDefinition() registry.ConditionDefinition {
	return registry.ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type:        supervisornode.ConditionTypeSupervisorRouteEquals,
			Title:       "Supervisor Route Equals",
			Description: "Routes to a worker when it matches the supervisor's selected member id.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"worker_id": dsl.JSONSchema{
						"type": "string", "title": "Worker ID", "description": "Member id configured on the Supervisor node.",
					},
				},
				"required":             []string{"worker_id"},
				"additionalProperties": false,
			},
			StatePorts: []dsl.StatePortDefinition{
				conditionCapabilityPort("supervisor", supervisorcap.CapabilityID, supervisorcap.FieldRoute),
			},
		},
		Resolve: func(resolved registry.ResolvedConditionSpec) (registry.EdgeCondition, error) {
			spec := resolved.Spec
			workerID := strings.TrimSpace(config.String(spec.Config, "worker_id"))
			if workerID == "" {
				return registry.EdgeCondition{}, fmt.Errorf("supervisor route condition requires worker_id")
			}
			path, err := resolvedConditionPath(resolved, "supervisor")
			if err != nil {
				return registry.EdgeCondition{}, err
			}
			return supervisornode.SupervisorRouteEquals(path, workerID), nil
		},
	}
}

func planStatusEqualsConditionDefinition() registry.ConditionDefinition {
	return registry.ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type:        plannode.ConditionTypePlanStatusEquals,
			Title:       "Plan Status Equals",
			Description: "Routes when shared planner state has the configured status.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"status": dsl.JSONSchema{
						"type": "string",
						"enum": []string{
							plannode.PlanStatusPlanning,
							plannode.PlanStatusExecuting,
							plannode.PlanStatusReplan,
							plannode.PlanStatusFinalizing,
							plannode.PlanStatusDone,
						},
					},
				},
				"required":             []string{"status"},
				"additionalProperties": false,
			},
			StatePorts: []dsl.StatePortDefinition{
				conditionCapabilityPort("plan", plancap.CapabilityID, plancap.FieldStatus),
			},
		},
		Resolve: func(resolved registry.ResolvedConditionSpec) (registry.EdgeCondition, error) {
			spec := resolved.Spec
			status := strings.ToLower(strings.TrimSpace(config.String(spec.Config, "status")))
			if status == "" {
				return registry.EdgeCondition{}, fmt.Errorf("plan status condition requires status")
			}
			path, err := resolvedConditionPath(resolved, "plan")
			if err != nil {
				return registry.EdgeCondition{}, err
			}
			return plannode.PlanStatusEquals(path, status), nil
		},
	}
}

func lastMessageHasToolCallsConditionDefinition() registry.ConditionDefinition {
	return registry.ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type:         ConditionTypeConversationHasToolCalls,
			Title:        "Last Message Has Tool Calls",
			Description:  "Routes when the last AI message includes tool calls.",
			ConfigSchema: dsl.JSONSchema{"type": "object", "additionalProperties": false},
			StatePorts: []dsl.StatePortDefinition{
				conditionCapabilityPort("conversation", conversationcap.CapabilityID, conversationcap.FieldMessages),
			},
		},
		Resolve: func(resolved registry.ResolvedConditionSpec) (registry.EdgeCondition, error) {
			path, err := resolvedConditionPath(resolved, "conversation")
			if err != nil {
				return registry.EdgeCondition{}, err
			}
			return ConversationHasToolCalls(path), nil
		},
	}
}

func hasFinalAnswerConditionDefinition() registry.ConditionDefinition {
	return registry.ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type:         ConditionTypeConversationHasFinalAnswer,
			Title:        "Has Final Answer",
			Description:  "Routes when the current state already contains a final answer.",
			ConfigSchema: dsl.JSONSchema{"type": "object", "additionalProperties": false},
			StatePorts: []dsl.StatePortDefinition{
				conditionCapabilityPort("conversation", conversationcap.CapabilityID, conversationcap.FieldFinalAnswer),
			},
		},
		Resolve: func(resolved registry.ResolvedConditionSpec) (registry.EdgeCondition, error) {
			path, err := resolvedConditionPath(resolved, "conversation")
			if err != nil {
				return registry.EdgeCondition{}, err
			}
			return ConversationHasFinalAnswer(path), nil
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
					"match": dsl.JSONSchema{"type": "string", "enum": []string{ExpressionMatchAll, ExpressionMatchAny}},
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
			StatePorts: []dsl.StatePortDefinition{
				{Name: "state", Description: "Object evaluated by relative expressions.", Required: true, Schema: dsl.JSONSchema{"type": "object"}, Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace},
			},
		},
		Resolve: func(resolved registry.ResolvedConditionSpec) (registry.EdgeCondition, error) {
			spec := resolved.Spec
			cfg, err := ParseExpressionConditionConfig(spec.Config)
			if err != nil {
				return registry.EdgeCondition{}, fmt.Errorf("resolve expression condition: %w", err)
			}
			path, err := resolvedConditionPath(resolved, "state")
			if err != nil {
				return registry.EdgeCondition{}, err
			}
			return ExpressionConditions(path, cfg)
		},
	}
}

func ConversationHasToolCalls(conversationPath state.Path) registry.EdgeCondition {
	spec := dsl.GraphConditionSpec{Type: ConditionTypeConversationHasToolCalls, State: map[string]dsl.StateBinding{"conversation": {Path: conversationPath.String()}}}
	return registry.NewEdgeCondition(spec, func(_ context.Context, current *state.State) (bool, error) {
		conversation, err := conversationcap.Bind(state.NewAccess(current), conversationPath)
		if err != nil {
			return false, err
		}
		messages := conversation.Messages()
		if len(messages) == 0 {
			return false, nil
		}
		lastMessage := messages[len(messages)-1]
		if lastMessage.Role != llms.ChatMessageTypeAI {
			return false, nil
		}
		for _, part := range lastMessage.Parts {
			if _, ok := part.(llms.ToolCall); ok {
				return true, nil
			}
		}
		return false, nil
	})
}

func ConversationHasFinalAnswer(conversationPath state.Path) registry.EdgeCondition {
	spec := dsl.GraphConditionSpec{Type: ConditionTypeConversationHasFinalAnswer, State: map[string]dsl.StateBinding{"conversation": {Path: conversationPath.String()}}}
	return registry.NewEdgeCondition(spec, func(_ context.Context, current *state.State) (bool, error) {
		conversation, err := conversationcap.Bind(state.NewAccess(current), conversationPath)
		if err != nil {
			return false, err
		}
		return conversation.FinalAnswer() != "", nil
	})
}

func conditionCapabilityPort(name, capabilityID string, field string) dsl.StatePortDefinition {
	return dsl.StatePortDefinition{
		Name: name, Required: true, Capability: capabilityID,
		Contract: dsl.RelativeStateContract{Fields: []dsl.RelativeStateFieldRef{{Path: field, Mode: dsl.StateAccessRead, Required: true}}},
	}
}

func resolvedConditionPath(spec registry.ResolvedConditionSpec, name string) (state.Path, error) {
	binding, ok := spec.State[name]
	if !ok || binding.Path.Empty() {
		return state.Path{}, fmt.Errorf("condition %q requires resolved state port %q", spec.Spec.Type, name)
	}
	return binding.Path, nil
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
	Match       string       `json:"match,omitempty"`
	Expressions []Expression `json:"expressions"`
}

func ExpressionConditions(rootPath state.Path, config ExpressionConditionConfig) (registry.EdgeCondition, error) {
	config = normalizeExpressionConditionConfig(config)
	if err := config.Validate(); err != nil {
		return registry.EdgeCondition{}, err
	}

	expressions := append([]Expression(nil), config.Expressions...)
	matchMode := config.Match

	return registry.NewEdgeCondition(dsl.GraphConditionSpec{
		Type:   ConditionTypeExpressionConditions,
		Config: config.Map(),
		State:  map[string]dsl.StateBinding{"state": {Path: rootPath.String()}},
	}, func(_ context.Context, current *state.State) (bool, error) {
		root, ok := state.NewAccess(current).ReadAny(rootPath)
		if !ok {
			return false, fmt.Errorf("condition state is missing at %q", rootPath)
		}
		switch matchMode {
		case ExpressionMatchAny:
			for _, expression := range expressions {
				if matchExpression(root, expression) {
					return true, nil
				}
			}
			return false, nil
		default:
			for _, expression := range expressions {
				if !matchExpression(root, expression) {
					return false, nil
				}
			}
			return true, nil
		}
	}), nil
}

func ParseExpressionConditionConfig(configMap map[string]any) (ExpressionConditionConfig, error) {
	parsed := ExpressionConditionConfig{
		Match: config.String(configMap, "match"),
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
	if isExplicitContractStatePath(expression.Value1) {
		return fmt.Errorf("expression value1 must be relative to the bound state port")
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

func matchExpression(root any, expression Expression) bool {
	expression = normalizeExpression(expression)
	if expression.Logic != "" {
		switch expression.Logic {
		case LogicAnd:
			for _, child := range expression.Children {
				if !matchExpression(root, child) {
					return false
				}
			}
			return true
		case LogicOr:
			for _, child := range expression.Children {
				if matchExpression(root, child) {
					return true
				}
			}
			return false
		case LogicNot:
			if len(expression.Children) != 1 {
				return false
			}
			return !matchExpression(root, expression.Children[0])
		default:
			return false
		}
	}
	left, ok := resolveExpressionValue(root, expression.Value1)
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

func resolveExpressionValue(root any, path string) (any, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, false
	}
	if path == "." || path == "$" {
		return root, true
	}
	segments := state.SplitStatePath(path)
	if len(segments) == 0 {
		return nil, false
	}
	return state.ResolveStateValue(root, segments)
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
