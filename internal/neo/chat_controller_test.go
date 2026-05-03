package neo

import (
	"testing"

	fruntime "weaveflow/runtime"
)

func TestFinalAnswerFromStatePrefersConversationAnswer(t *testing.T) {
	t.Parallel()

	state := fruntime.State{}
	state.Conversation(stateScope).SetFinalAnswer("conversation answer")
	state.Ensure(fruntime.StateKeyFinal)["answer"] = "final state answer"

	if got := finalAnswerFromState(state); got != "conversation answer" {
		t.Fatalf("finalAnswerFromState() = %q, want %q", got, "conversation answer")
	}
}

func TestFinalAnswerFromStateFallsBackToFinalState(t *testing.T) {
	t.Parallel()

	state := fruntime.State{}
	state.Ensure(fruntime.StateKeyFinal)["answer"] = "final state answer"

	if got := finalAnswerFromState(state); got != "final state answer" {
		t.Fatalf("finalAnswerFromState() = %q, want %q", got, "final state answer")
	}
}
