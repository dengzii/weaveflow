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

func TestPlanStepExecutorTreatsInProgressStepWithResultAsDependencySatisfied(t *testing.T) {
	t.Parallel()

	node := NewPlanStepExecutorNode()

	state := fruntime.State{
		fruntime.StateKeyPlanner: map[string]any{
			"status": "executing",
			"plan": []any{
				map[string]any{
					"id":     "step_a",
					"title":  "Step A",
					"status": "in_progress",
					"kind":   "research",
				},
				map[string]any{
					"id":         "step_b",
					"title":      "Step B",
					"status":     "pending",
					"kind":       "decision",
					"depends_on": []any{"step_a"},
				},
			},
		},
		fruntime.StateKeyExecution: map[string]any{
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

	plannerState := state.Get(fruntime.StateKeyPlanner)
	if got := plannerState["current_step_id"]; got != "step_b" {
		t.Fatalf("expected step_b to be selected, got %#v", got)
	}

	exec := state.Get(fruntime.StateKeyExecution)
	if got := exec["route"]; got != ExecutionRouteLLM {
		t.Fatalf("expected route llm, got %#v", got)
	}
}

func TestPlanStepExecutorBlockedDiagnosticsIncludeMissingDependencies(t *testing.T) {
	t.Parallel()

	node := NewPlanStepExecutorNode()

	state := fruntime.State{
		fruntime.StateKeyPlanner: map[string]any{
			"status": "executing",
			"plan": []any{
				map[string]any{
					"id":     "step_a",
					"title":  "Step A",
					"status": "blocked",
					"kind":   "research",
				},
				map[string]any{
					"id":         "step_b",
					"title":      "Step B",
					"status":     "pending",
					"kind":       "decision",
					"depends_on": []any{"step_a"},
				},
			},
		},
	}

	state, err := node.Invoke(context.Background(), state)
	if err != nil {
		t.Fatalf("invoke plan step executor: %v", err)
	}

	exec := state.Get(fruntime.StateKeyExecution)
	if got := exec["route"]; got != ExecutionRouteBlocked {
		t.Fatalf("expected route blocked, got %#v", got)
	}

	diagnostics, ok := exec["blocked_diagnostics"].(map[string]any)
	if !ok {
		t.Fatalf("expected blocked_diagnostics map, got %T", exec["blocked_diagnostics"])
	}

	waitingSteps, ok := diagnostics["waiting_steps"].([]map[string]any)
	if !ok || len(waitingSteps) == 0 {
		t.Fatalf("expected waiting_steps diagnostics, got %#v", diagnostics["waiting_steps"])
	}

	missingDeps, ok := waitingSteps[0]["missing_deps"].([]map[string]any)
	if !ok || len(missingDeps) != 1 {
		t.Fatalf("expected missing_deps diagnostics, got %#v", waitingSteps[0]["missing_deps"])
	}
	if got := missingDeps[0]["step_id"]; got != "step_a" {
		t.Fatalf("expected missing dependency step_a, got %#v", got)
	}
	if got := missingDeps[0]["status"]; got != "blocked" {
		t.Fatalf("expected missing dependency status blocked, got %#v", got)
	}
}
