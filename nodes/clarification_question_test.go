package nodes

import (
	"context"
	"errors"
	"testing"
	wfstate "weaveflow/state"

	langgraph "github.com/smallnest/langgraphgo/graph"
)

func TestClarificationQuestionNodeInterruptsWhenPending(t *testing.T) {
	t.Parallel()

	state := wfstate.State{}
	orchestration := state.Ensure(wfstate.StateKeyOrchestration)
	orchestration["needs_clarification"] = true
	orchestration["clarification_question"] = "Which file?"
	orchestration["clarification_options"] = []string{"A.go", "B.go"}

	node := NewClarificationQuestionNode()

	_, err := runTestNode(t, node, context.Background(), state)
	if err == nil {
		t.Fatal("expected NodeInterrupt error")
	}
	var interrupt *langgraph.NodeInterrupt
	if !errors.As(err, &interrupt) {
		t.Fatalf("expected *langgraph.NodeInterrupt, got %T: %v", err, err)
	}
	if interrupt.Node != node.NodeID {
		t.Fatalf("interrupt node mismatch: got %q want %q", interrupt.Node, node.NodeID)
	}
}

func TestClarificationQuestionNodeAppliesUserChoice(t *testing.T) {
	t.Parallel()

	state := wfstate.State{}
	request := state.Ensure(wfstate.StateKeyRequest)
	request["input"] = "do the thing"
	orchestration := state.Ensure(wfstate.StateKeyOrchestration)
	orchestration["needs_clarification"] = true
	orchestration["clarification_question"] = "Which thing?"
	orchestration["clarification_options"] = []string{"a", "b"}

	state[ClarificationStateKey] = wfstate.State{
		ClarificationUserChoiceKey: "the second one",
	}

	node := NewClarificationQuestionNode()
	next, err := runTestNode(t, node, context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotOrch := next.Get(wfstate.StateKeyOrchestration)
	if gotOrch == nil {
		t.Fatal("orchestration state missing after resume")
	}
	if needs, _ := gotOrch["needs_clarification"].(bool); needs {
		t.Fatal("needs_clarification should be cleared after applying user choice")
	}
	if attempts, _ := gotOrch["clarification_attempts"].(int); attempts != 1 {
		t.Fatalf("expected clarification_attempts=1, got %v", gotOrch["clarification_attempts"])
	}
	choice, _ := gotOrch["last_clarification_choice"].(string)
	if choice != "the second one" {
		t.Fatalf("expected last_clarification_choice to be recorded, got %q", choice)
	}

	gotReq := next.Get(wfstate.StateKeyRequest)
	if gotReq == nil {
		t.Fatal("request state missing after resume")
	}
	input, _ := gotReq["input"].(string)
	if input == "do the thing" || input == "" {
		t.Fatalf("expected request.input to be rewritten with clarification context, got %q", input)
	}
}

func TestClarificationQuestionNodeExhaustsAfterMaxAttempts(t *testing.T) {
	t.Parallel()

	state := wfstate.State{}
	orchestration := state.Ensure(wfstate.StateKeyOrchestration)
	orchestration["needs_clarification"] = true
	orchestration["clarification_attempts"] = 2
	orchestration["clarification_question"] = "Still unclear"

	node := NewClarificationQuestionNode()
	next, err := runTestNode(t, node, context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error when exhausted: %v", err)
	}
	gotOrch := next.Get(wfstate.StateKeyOrchestration)
	if exhausted, _ := gotOrch["clarification_exhausted"].(bool); !exhausted {
		t.Fatalf("expected clarification_exhausted=true, got %v", gotOrch["clarification_exhausted"])
	}
}
