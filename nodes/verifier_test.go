package nodes

import (
	"context"
	"testing"
	"weaveflow/core"

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

	if _, err := runTestNode(t, executor, context.Background(), state); err != nil {
		t.Fatalf("executor invoke failed: %v", err)
	}
}

func TestVerifierResearchPartialRetryStaysAsRetry(t *testing.T) {
	t.Parallel()

	// Under the strict policy, partial+retry must NOT be silently coerced to
	// continue. The step stays in retry until the retry budget is exhausted,
	// at which point applyResultWithContext promotes it to replan.
	model := &captureLLMModel{response: `{
		"status": "partial",
		"issues": ["one non-critical criterion is not fully covered"],
		"summary": "Useful research evidence was collected, with one gap remaining.",
		"suggestion": "retry"
	}`}
	node := NewVerifierNode()
	node.StateScope = "agent"

	state := wfstate.State{
		wfstate.StateKeyPlanner: map[string]any{
			"current_step_id": "step_1",
			"plan": []map[string]any{
				{
					"id":                  "step_1",
					"title":               "Inspect state keys",
					"status":              "in_progress",
					"kind":                "research",
					"acceptance_criteria": []string{"State keys are summarized.", "Path helpers are mentioned."},
				},
			},
		},
	}
	state.AppendObservation(map[string]any{
		"step_id": "step_1",
		"source":  "ai",
		"summary": "Read state/keys.go and identified the project state key naming conventions.",
	})

	ctx := core.WithServices(context.Background(), &core.Services{Model: model})
	next, err := runTestNode(t, node, ctx, state)
	if err != nil {
		t.Fatalf("invoke verifier: %v", err)
	}

	verification := next.Get(wfstate.StateKeyVerification)
	if got := verification["next_action"]; got != VerificationActionRetry {
		t.Fatalf("expected research partial to stay as retry, got %#v", got)
	}

	plan, _ := next.Get(wfstate.StateKeyPlanner)["plan"].([]map[string]any)
	if got := plan[0]["status"]; got != "ready" {
		t.Fatalf("expected current research step to be marked ready for retry, got %#v", got)
	}
}

func TestVerifierResearchIterationLimitRetriesReplan(t *testing.T) {
	t.Parallel()

	model := &captureLLMModel{response: `{
		"status": "fail",
		"issues": ["Maximum tool iterations reached before analysis could be completed - only file structure was discovered"],
		"summary": "The research step was too broad for the available tool budget.",
		"suggestion": "retry"
	}`}
	node := NewVerifierNode()
	node.StateScope = "agent"

	state := wfstate.State{
		wfstate.StateKeyPlanner: map[string]any{
			"current_step_id": "step_1",
			"plan": []map[string]any{
				{
					"id":                  "step_1",
					"title":               "Inspect persistence and cleanup",
					"status":              "in_progress",
					"kind":                "research",
					"acceptance_criteria": []string{"Persistence is explained.", "Cleanup is explained."},
				},
			},
		},
	}

	ctx := core.WithServices(context.Background(), &core.Services{Model: model})
	next, err := runTestNode(t, node, ctx, state)
	if err != nil {
		t.Fatalf("invoke verifier: %v", err)
	}

	verification := next.Get(wfstate.StateKeyVerification)
	if got := verification["next_action"]; got != VerificationActionReplan {
		t.Fatalf("expected iteration limit retry to become replan, got %#v", got)
	}

	plan, _ := next.Get(wfstate.StateKeyPlanner)["plan"].([]map[string]any)
	if got := plan[0]["status"]; got != "blocked" {
		t.Fatalf("expected current research step to be blocked for replanning, got %#v", got)
	}
	if _, exists := plan[0]["retry_count"]; exists {
		t.Fatalf("did not expect retry_count to increment when converting to replan, got %#v", plan[0]["retry_count"])
	}
}

// TestVerifierResearchPathMismatchShortcuts the path-mismatch programmatic
// guard: when a research step declares file inputs but none of them appears
// in any observation summary, verifier must skip the LLM judgment and
// return fail+replan directly so we don't waste retries.
func TestVerifierResearchPathMismatchShortcuts(t *testing.T) {
	t.Parallel()

	node := NewVerifierNode()
	node.StateScope = "agent"

	state := wfstate.State{
		wfstate.StateKeyPlanner: map[string]any{
			"current_step_id": "step_1",
			"plan": []map[string]any{
				{
					"id":                  "step_1",
					"title":               "Read state/keys.go",
					"status":              "in_progress",
					"kind":                "research",
					"inputs":              []string{"state/keys.go"},
					"acceptance_criteria": []string{"Key constants are extracted."},
				},
			},
		},
	}
	// Observation references a DIFFERENT file — path mismatch.
	state.AppendObservation(map[string]any{
		"step_id": "step_1",
		"source":  "tool:read",
		"summary": "state/snapshot.go\n1\tpackage state\n...",
	})

	// No model needed — the shortcut returns before the LLM call.
	ctx := core.WithServices(context.Background(), &core.Services{})
	next, err := runTestNode(t, node, ctx, state)
	if err != nil {
		t.Fatalf("invoke verifier: %v", err)
	}

	verification := next.Get(wfstate.StateKeyVerification)
	if got := verification["status"]; got != VerificationFail {
		t.Fatalf("expected path mismatch to fail, got status %#v", got)
	}
	if got := verification["next_action"]; got != VerificationActionReplan {
		t.Fatalf("expected path mismatch to trigger replan, got %#v", got)
	}
}

// TestVerifierResearchPathMatchDoesNotShortcut ensures the guard does NOT
// fire when observations actually reference the declared input.
func TestVerifierResearchPathMatchDoesNotShortcut(t *testing.T) {
	t.Parallel()

	model := &captureLLMModel{response: `{"status":"pass","issues":[],"summary":"ok","suggestion":"continue"}`}
	node := NewVerifierNode()
	node.StateScope = "agent"

	state := wfstate.State{
		wfstate.StateKeyPlanner: map[string]any{
			"current_step_id": "step_1",
			"plan": []map[string]any{
				{
					"id":                  "step_1",
					"title":               "Read state/keys.go",
					"status":              "in_progress",
					"kind":                "research",
					"inputs":              []string{"state/keys.go"},
					"acceptance_criteria": []string{"Key constants are extracted."},
				},
			},
		},
	}
	state.AppendObservation(map[string]any{
		"step_id": "step_1",
		"source":  "tool:read",
		"summary": "state/keys.go\n1\tpackage state\nconst StateKeyRequest = \"request\"\n",
	})

	ctx := core.WithServices(context.Background(), &core.Services{Model: model})
	next, err := runTestNode(t, node, ctx, state)
	if err != nil {
		t.Fatalf("invoke verifier: %v", err)
	}

	verification := next.Get(wfstate.StateKeyVerification)
	if got := verification["status"]; got != VerificationPass {
		t.Fatalf("expected path match to reach LLM pass, got status %#v issues=%v", got, verification["issues"])
	}
}
