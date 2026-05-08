package nodes

import (
	"context"
	"testing"
	fruntime "weaveflow/runtime"
)

func TestPlanStepExecutorHonorsPlannerPathAndDependencyStatus(t *testing.T) {
	t.Parallel()

	node := NewPlanStepExecutorNode()
	node.PlannerStatePath = "custom.planner"

	state := fruntime.State{
		"custom": map[string]any{
			"planner": map[string]any{
				"status": "executing",
				"plan": []any{
					map[string]any{
						"id":         "step_b",
						"title":      "Step B",
						"status":     "pending",
						"kind":       "decision",
						"depends_on": []any{"step_a"},
					},
					map[string]any{
						"id":     "step_a",
						"title":  "Step A",
						"status": "ready",
						"kind":   "research",
					},
				},
			},
		},
		"execution": map[string]any{
			"step_results": map[string]any{
				"step_a": map[string]any{
					"observations_count": 1,
				},
			},
		},
	}

	state, err := node.Invoke(context.Background(), state)
	if err != nil {
		t.Fatalf("invoke plan step executor: %v", err)
	}

	plannerState := stateObjectAtPath(state, "custom.planner")
	if plannerState == nil {
		t.Fatal("expected custom planner state")
	}
	if got := plannerState["current_step_id"]; got != "step_a" {
		t.Fatalf("expected step_a to be selected, got %#v", got)
	}

	exec := state.Get(fruntime.StateKeyExecution)
	if exec == nil {
		t.Fatal("expected execution state")
	}
	currentStep, ok := exec["current_step"].(map[string]any)
	if !ok {
		t.Fatalf("expected current_step map, got %T", exec["current_step"])
	}
	if got := currentStep["id"]; got != "step_a" {
		t.Fatalf("expected current step step_a, got %#v", got)
	}
	if got := currentStep["status"]; got != "in_progress" {
		t.Fatalf("expected current step status in_progress, got %#v", got)
	}
	if got := exec["route"]; got != ExecutionRouteLLM {
		t.Fatalf("expected route llm, got %#v", got)
	}
}
