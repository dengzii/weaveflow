package neo

import (
	"testing"

	"weaveflow/core"
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

func TestNewChatRunnerDefaultsToStrictContractValidation(t *testing.T) {
	t.Parallel()

	runner := newChatRunner(nil, "graph-id", t.TempDir(), nil)
	if runner.ContractValidation != core.ContractValidationStrict {
		t.Fatalf("ContractValidation = %q, want %q", runner.ContractValidation, core.ContractValidationStrict)
	}
	if runner.GraphID != "graph-id" {
		t.Fatalf("GraphID = %q, want %q", runner.GraphID, "graph-id")
	}
	if runner.ArtifactStore == nil {
		t.Fatal("expected artifact store to be configured")
	}
}
