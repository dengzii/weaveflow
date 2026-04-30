package weaveflow

import (
	"context"
	"strings"
	"testing"
	fruntime "weaveflow/runtime"
)

func TestExpressionConditionsMatchAllAgainstScopedStateAndConversation(t *testing.T) {
	t.Parallel()

	state := State{
		"status": "root",
	}
	scope := state.EnsureScope("agent")
	scope["status"] = "ready"
	scope["tags"] = []string{"tool", "final"}
	state.Conversation("agent").SetFinalAnswer("done")

	condition, err := ExpressionConditions(ExpressionConditionConfig{
		StateScope: "agent",
		Expressions: []Expression{
			{Value1: "status", Op: OperationEqual, Value2: "ready"},
			{Value1: "tags", Op: OperationContains, Value2: "final"},
			{Value1: fruntime.StateKeyFinalAnswer, Op: OperationEqual, Value2: "done"},
		},
	})
	if err != nil {
		t.Fatalf("build expression condition: %v", err)
	}

	if !condition.Match(context.Background(), state) {
		t.Fatal("expected expression condition to match")
	}
}

func TestExpressionConditionsMatchAny(t *testing.T) {
	t.Parallel()

	state := State{
		"status": "running",
	}

	condition, err := ExpressionConditions(ExpressionConditionConfig{
		Match: ExpressionMatchAny,
		Expressions: []Expression{
			{Value1: "status", Op: OperationEqual, Value2: "done"},
			{Value1: "status", Op: OperationEqual, Value2: "running"},
		},
	})
	if err != nil {
		t.Fatalf("build expression condition: %v", err)
	}

	if !condition.Match(context.Background(), state) {
		t.Fatal("expected any-match expression condition to match")
	}
}

func TestParseExpressionConditionConfigFromSerializableConfig(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	condition, err := registry.ResolveCondition(GraphConditionSpec{
		Type: "expression_conditions",
		Config: map[string]any{
			"state_scope": "agent",
			"match":       "all",
			"expressions": []any{
				map[string]any{
					"value1": "result.code",
					"op":     "equals",
					"value2": "200",
				},
				map[string]any{
					"value1": "final_answer",
					"op":     "contains",
					"value2": "success",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("resolve condition: %v", err)
	}

	state := State{}
	scope := state.EnsureScope("agent")
	scope["result"] = map[string]any{
		"code": 200,
	}
	state.Conversation("agent").SetFinalAnswer("success")

	if !condition.Match(context.Background(), state) {
		t.Fatal("expected resolved expression condition to match")
	}
}

func TestParseExpressionConditionConfigRejectsInvalidExpression(t *testing.T) {
	t.Parallel()

	_, err := ParseExpressionConditionConfig(map[string]any{
		"expressions": []any{
			map[string]any{
				"value1": "status",
				"op":     "bad_op",
				"value2": "done",
			},
		},
	})
	if err == nil {
		t.Fatal("expected invalid op error")
	}
	if !strings.Contains(err.Error(), "bad_op") {
		t.Fatalf("expected invalid op in error, got %v", err)
	}
}
