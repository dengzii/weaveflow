package nodes

import (
	"context"
	"strings"
	"testing"
	wfstate "weaveflow/state"
)

func TestFinalizerUsesExecutionRouteForBlockedOutcome(t *testing.T) {
	t.Parallel()

	node := NewFinalizerNode()
	state := wfstate.State{
		wfstate.StateKeyVerification: wfstate.State{
			"status":      VerificationInconclusive,
			"next_action": VerificationActionContinue,
			"summary":     "Waiting on an external dependency.",
		},
		wfstate.StateKeyExecution: wfstate.State{
			"route": ExecutionRouteBlocked,
		},
	}

	state, err := runTestNode(t, node, context.Background(), state)
	if err != nil {
		t.Fatalf("invoke finalizer: %v", err)
	}

	final := state.Get(wfstate.StateKeyFinal)
	if final == nil {
		t.Fatal("expected final state")
	}
	if got := final["status"]; got != FinalStatusBlocked {
		t.Fatalf("expected blocked final status, got %#v", got)
	}
	answer, _ := final["answer"].(string)
	if !strings.Contains(strings.ToLower(answer), "blocked") {
		t.Fatalf("expected blocked answer, got %#v", answer)
	}
}

func TestFinalizerUsesOrchestrationClarificationOutcome(t *testing.T) {
	t.Parallel()

	node := NewFinalizerNode()
	state := wfstate.State{
		wfstate.StateKeyOrchestration: wfstate.State{
			"mode":                   "planner",
			"needs_clarification":    true,
			"clarification_question": "Do you want only the orchestration diagnosis, or code changes as well?",
			"reasoning":              "The requested scope is ambiguous.",
		},
	}

	state, err := runTestNode(t, node, context.Background(), state)
	if err != nil {
		t.Fatalf("invoke finalizer: %v", err)
	}

	final := state.Get(wfstate.StateKeyFinal)
	if final == nil {
		t.Fatal("expected final state")
	}
	if got := final["status"]; got != FinalStatusNeedsClarification {
		t.Fatalf("expected clarification final status, got %#v", got)
	}
	answer, _ := final["answer"].(string)
	if !strings.Contains(answer, "Do you want only the orchestration diagnosis, or code changes as well?") {
		t.Fatalf("expected clarification question in answer, got %#v", answer)
	}
}
