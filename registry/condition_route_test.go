package registry

import (
	"context"
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
