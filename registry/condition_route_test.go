package registry

import (
	"context"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/state"
)

func TestRouteDecisionValidation(t *testing.T) {
	condition := NewEdgeCondition(dsl.GraphConditionSpec{Type: "test"}, func(context.Context, *state.State) (RouteDecision, error) {
		return RouteDecision{Matched: true, Reason: " selected ", Details: map[string]any{"score": 1}}, nil
	})
	decision, err := condition.EvaluateRoute(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("evaluate route: %v", err)
	}
	if !decision.Matched || decision.Reason != "selected" {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestRouteDecisionRejectsDetailsWithoutReason(t *testing.T) {
	condition := NewEdgeCondition(dsl.GraphConditionSpec{Type: "test"}, func(context.Context, *state.State) (RouteDecision, error) {
		return RouteDecision{Details: map[string]any{"score": 1}}, nil
	})
	if _, err := condition.EvaluateRoute(context.Background(), state.NewState()); err == nil {
		t.Fatal("expected invalid decision error")
	}
}

func TestRouteDecisionRejectsConflictingTargetsAndSends(t *testing.T) {
	condition := NewEdgeCondition(dsl.GraphConditionSpec{Type: "test"}, func(context.Context, *state.State) (RouteDecision, error) {
		return RouteDecision{
			Matched: true,
			Targets: []core.NodeRef{"target"},
			Send:    []core.Send{{Target: "worker"}},
		}, nil
	})
	if _, err := condition.EvaluateRoute(context.Background(), state.NewState()); err == nil {
		t.Fatal("expected conflicting route decision error")
	}
}

func TestRouteDecisionPreservesUnmatchedDiagnostic(t *testing.T) {
	condition := NewEdgeCondition(dsl.GraphConditionSpec{Type: "test"}, func(context.Context, *state.State) (RouteDecision, error) {
		return RouteDecision{Reason: " threshold not met ", Details: map[string]any{"score": 0.2}}, nil
	})
	decision, err := condition.EvaluateRoute(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("evaluate route: %v", err)
	}
	if decision.Matched || decision.Reason != "threshold not met" || decision.Details["score"] != 0.2 {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestRouteDecisionIsolatesMutableOutput(t *testing.T) {
	details := map[string]any{"nested": map[string]any{"value": "source"}}
	inputValue := map[string]any{"items": []any{"source"}}
	condition := NewEdgeCondition(dsl.GraphConditionSpec{Type: "test"}, func(context.Context, *state.State) (RouteDecision, error) {
		return RouteDecision{
			Matched: true,
			Reason:  "isolated",
			Details: details,
			Send: []core.Send{{Target: "worker", Input: state.NewPatch(state.PatchOp{
				Kind: state.OpSet, Path: state.Shared("input"), Value: inputValue,
			})}},
		}, nil
	})
	decision, err := condition.EvaluateRoute(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("EvaluateRoute() error = %v", err)
	}
	details["nested"].(map[string]any)["value"] = "mutated"
	inputValue["items"].([]any)[0] = "mutated"
	if got := decision.Details["nested"].(map[string]any)["value"]; got != "source" {
		t.Fatalf("decision details value = %#v, want source", got)
	}
	operations := decision.Send[0].Input.Ops()
	if got := operations[0].Value.(map[string]any)["items"].([]any)[0]; got != "source" {
		t.Fatalf("decision send input = %#v, want source", got)
	}
}

func TestRouteDecisionRejectsOpaqueMutableOutput(t *testing.T) {
	condition := NewEdgeCondition(dsl.GraphConditionSpec{Type: "test"}, func(context.Context, *state.State) (RouteDecision, error) {
		return RouteDecision{Matched: true, Reason: "opaque", Details: map[string]any{"opaque": &routeOpaqueValue{values: []string{"source"}}}}, nil
	})
	_, err := condition.EvaluateRoute(context.Background(), state.NewState())
	if err == nil || !strings.Contains(err.Error(), "cannot be safely cloned") {
		t.Fatalf("EvaluateRoute() error = %v, want strict clone rejection", err)
	}
}

type routeOpaqueValue struct {
	values []string
}
