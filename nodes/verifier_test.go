package nodes

import (
	"context"
	"testing"

	wfstate "weaveflow/state"
)

func TestVerifierContinueMarksCurrentStepCompleted(t *testing.T) {
	t.Parallel()

	node := NewVerifierNode()

	state := wfstate.State{
		wfstate.StateKeyPlanner: map[string]any{
			"current_step_id": "step_1",
			"plan": []any{
				map[string]any{
					"id":     "step_1",
					"status": "in_progress",
				},
			},
		},
	}

	node.applyResult(state, &verificationResult{
		Status:     VerificationInconclusive,
		Summary:    "No observations to verify against criteria.",
		NextAction: VerificationActionContinue,
	}, VerifierModeStep)

	planner := state.Get(wfstate.StateKeyPlanner)
	plan, _ := planner["plan"].([]any)
	step, _ := plan[0].(map[string]any)
	if got := step["status"]; got != "completed" {
		t.Fatalf("expected current step to be marked completed on continue, got %#v", got)
	}
}

func TestPlanStepExecutorAdvancesAfterVerifierContinue(t *testing.T) {
	t.Parallel()

	verifier := NewVerifierNode()
	executor := NewPlanStepExecutorNode()

	state := wfstate.State{
		wfstate.StateKeyPlanner: map[string]any{
			"current_step_id": "step_1",
			"plan": []any{
				map[string]any{
					"id":     "step_1",
					"title":  "Step 1",
					"status": "in_progress",
					"kind":   "research",
				},
				map[string]any{
					"id":         "step_2",
					"title":      "Step 2",
					"status":     "pending",
					"kind":       "decision",
					"depends_on": []any{"step_1"},
				},
			},
		},
	}

	verifier.applyResult(state, &verificationResult{
		Status:     VerificationInconclusive,
		Summary:    "No observations to verify against criteria.",
		NextAction: VerificationActionContinue,
	}, VerifierModeStep)

	next, reason := selectNextStep(extractPlanSteps(state.Get(wfstate.StateKeyPlanner)), state.StepResults())
	if next == nil {
		t.Fatalf("expected next step to be selectable, got nil with reason %q", reason)
	}
	if got := next["id"]; got != "step_2" {
		t.Fatalf("expected dependent step_2 to be selected, got %#v", got)
	}

	if _, err := executor.Invoke(context.Background(), state); err != nil {
		t.Fatalf("executor invoke failed: %v", err)
	}
}
