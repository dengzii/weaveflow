package neo

import (
	"testing"

	wfstate "weaveflow/state"
)

func TestFinalAnswerFromStatePrefersConversationAnswer(t *testing.T) {
	t.Parallel()

	state := wfstate.State{}
	state.Conversation(stateScope).SetFinalAnswer("conversation answer")
	state.Ensure(wfstate.StateKeyFinal)["answer"] = "final state answer"

	if got := finalAnswerFromState(state); got != "conversation answer" {
		t.Fatalf("finalAnswerFromState() = %q, want %q", got, "conversation answer")
	}
}

func TestFinalAnswerFromStateFallsBackToFinalState(t *testing.T) {
	t.Parallel()

	state := wfstate.State{}
	state.Ensure(wfstate.StateKeyFinal)["answer"] = "final state answer"

	if got := finalAnswerFromState(state); got != "final state answer" {
		t.Fatalf("finalAnswerFromState() = %q, want %q", got, "final state answer")
	}
}
