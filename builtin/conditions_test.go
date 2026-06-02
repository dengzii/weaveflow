package builtin

import (
	"context"
	"strings"
	"testing"
	"weaveflow/dsl"
	wfstate "weaveflow/state"
)

func TestExpressionConditionsMatchAllAgainstScopedStateAndConversation(t *testing.T) {
	t.Parallel()

	state := wfstate.State{
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
			{Value1: wfstate.KeyFinalAnswer, Op: OperationEqual, Value2: "done"},
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

	state := wfstate.State{
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

	registry := NewDefaultRegistry()
	condition, err := registry.ResolveCondition(dsl.GraphConditionSpec{
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

	state := wfstate.State{}
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

func TestExpressionConditionsNestedAndOrNot(t *testing.T) {
	t.Parallel()

	state := wfstate.State{}
	scope := state.EnsureScope("agent")
	scope["status"] = "ready"
	scope["retries"] = "2"
	scope["tags"] = []string{"tool"}

	// (status == "ready" AND retries == "2") OR NOT(tags contains "final")
	condition, err := ExpressionConditions(ExpressionConditionConfig{
		StateScope: "agent",
		Expressions: []Expression{{
			Logic: LogicOr,
			Children: []Expression{
				{
					Logic: LogicAnd,
					Children: []Expression{
						{Value1: "status", Op: OperationEqual, Value2: "ready"},
						{Value1: "retries", Op: OperationEqual, Value2: "2"},
					},
				},
				{
					Logic: LogicNot,
					Children: []Expression{
						{Value1: "tags", Op: OperationContains, Value2: "final"},
					},
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("build nested expression: %v", err)
	}
	if !condition.Match(context.Background(), state) {
		t.Fatal("expected nested expression to match (and-branch satisfied)")
	}

	// Flip status so the and-branch fails; not-branch still true since tags lacks "final".
	scope["status"] = "blocked"
	if !condition.Match(context.Background(), state) {
		t.Fatal("expected nested expression to match via NOT branch")
	}

	// Add "final" tag so NOT branch fails too — whole expression should now be false.
	scope["tags"] = []string{"tool", "final"}
	if condition.Match(context.Background(), state) {
		t.Fatal("expected nested expression to fail when both branches are false")
	}
}

func TestExpressionConditionsParseCompositeFromConfig(t *testing.T) {
	t.Parallel()

	registry := NewDefaultRegistry()
	condition, err := registry.ResolveCondition(dsl.GraphConditionSpec{
		Type: "expression_conditions",
		Config: map[string]any{
			"state_scope": "agent",
			"expressions": []any{
				map[string]any{
					"logic": "or",
					"children": []any{
						map[string]any{"value1": "status", "op": "equals", "value2": "done"},
						map[string]any{
							"logic": "and",
							"children": []any{
								map[string]any{"value1": "status", "op": "equals", "value2": "ready"},
								map[string]any{
									"logic": "not",
									"children": []any{
										map[string]any{"value1": "tags", "op": "contains", "value2": "final"},
									},
								},
							},
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("resolve nested condition: %v", err)
	}

	state := wfstate.State{}
	scope := state.EnsureScope("agent")
	scope["status"] = "ready"
	scope["tags"] = []string{"tool"}

	if !condition.Match(context.Background(), state) {
		t.Fatal("expected parsed nested condition to match")
	}

	scope["tags"] = []string{"tool", "final"}
	if condition.Match(context.Background(), state) {
		t.Fatal("expected parsed nested condition to fail when NOT branch fails")
	}
}

func TestExpressionConditionsRejectMalformedComposite(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		expr Expression
	}{
		{
			name: "unknown logic",
			expr: Expression{Logic: "xor", Children: []Expression{{Value1: "a", Op: OperationEqual, Value2: "b"}}},
		},
		{
			name: "composite with leaf fields",
			expr: Expression{Logic: LogicAnd, Value1: "a", Op: OperationEqual, Value2: "b", Children: []Expression{{Value1: "x", Op: OperationEqual, Value2: "y"}}},
		},
		{
			name: "composite without children",
			expr: Expression{Logic: LogicAnd},
		},
		{
			name: "not with multiple children",
			expr: Expression{Logic: LogicNot, Children: []Expression{
				{Value1: "a", Op: OperationEqual, Value2: "b"},
				{Value1: "c", Op: OperationEqual, Value2: "d"},
			}},
		},
		{
			name: "leaf with children",
			expr: Expression{Value1: "a", Op: OperationEqual, Value2: "b", Children: []Expression{
				{Value1: "c", Op: OperationEqual, Value2: "d"},
			}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ExpressionConditions(ExpressionConditionConfig{Expressions: []Expression{tc.expr}}); err == nil {
				t.Fatalf("expected validation error for %s", tc.name)
			}
		})
	}
}

func TestExpressionConditionsResolveExplicitCanonicalPaths(t *testing.T) {
	t.Parallel()

	state := wfstate.State{
		"status": "root",
	}
	state.EnsureScope("agent")["status"] = "ready"
	state.EnsureNamespace("runtime")["loop"] = map[string]any{
		"done": false,
	}
	state.Conversation("").SetFinalAnswer("done")

	condition, err := ExpressionConditions(ExpressionConditionConfig{
		StateScope: "agent",
		Expressions: []Expression{
			{Value1: "shared.status", Op: OperationEqual, Value2: "root"},
			{Value1: "scopes.agent.status", Op: OperationEqual, Value2: "ready"},
			{Value1: "runtime.loop.done", Op: OperationEqual, Value2: "false"},
			{Value1: "conversation.final_answer", Op: OperationEqual, Value2: "done"},
		},
	})
	if err != nil {
		t.Fatalf("build expression condition: %v", err)
	}

	if !condition.Match(context.Background(), state) {
		t.Fatal("expected explicit canonical paths to resolve")
	}
}
