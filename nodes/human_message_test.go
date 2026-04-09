package nodes

import (
	"context"
	"testing"
	fruntime "weaveflow/runtime"

	"github.com/tmc/langchaingo/llms"
)

func TestHumanMessageNodeConsumesPendingHumanInput(t *testing.T) {
	t.Parallel()

	state := fruntime.State{}
	scope := state.EnsureScope("agent")
	scope[PendingHumanInputStateKey] = "approved"
	fruntime.Conversation(state, "agent").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeAI, "need human input"),
	})

	node := NewHumanMessageNode()
	node.StateScope = "agent"

	next, err := node.Invoke(context.Background(), state)
	if err != nil {
		t.Fatalf("invoke human message nodes: %v", err)
	}
	if next == nil {
		t.Fatal("expected state to be returned")
	}
	if _, ok := scope[PendingHumanInputStateKey]; ok {
		t.Fatal("expected pending human input to be consumed")
	}

	messages := fruntime.Conversation(state, "agent").Messages()
	if len(messages) != 2 {
		t.Fatalf("expected two messages, got %#v", messages)
	}
	if messages[1].Role != llms.ChatMessageTypeHuman {
		t.Fatalf("expected appended human message, got %#v", messages[1].Role)
	}
}

func TestHumanMessageNodeInterruptsWithoutPendingHumanInput(t *testing.T) {
	t.Parallel()

	state := fruntime.State{}
	fruntime.Conversation(state, "agent").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeAI, "need human input"),
	})

	node := NewHumanMessageNode()
	node.StateScope = "agent"

	_, err := node.Invoke(context.Background(), state)
	if err == nil {
		t.Fatal("expected interrupt error")
	}
}

func TestHumanMessageNodeDefaultsToRootConversation(t *testing.T) {
	t.Parallel()

	state := fruntime.State{
		PendingHumanInputStateKey: "approved",
	}
	fruntime.Conversation(state, "").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeAI, "need human input"),
	})

	node := NewHumanMessageNode()

	_, err := node.Invoke(context.Background(), state)
	if err != nil {
		t.Fatalf("invoke default-scope human message node: %v", err)
	}

	messages := fruntime.Conversation(state, "").Messages()
	if len(messages) != 2 {
		t.Fatalf("expected two root messages, got %#v", messages)
	}
	if messages[1].Role != llms.ChatMessageTypeHuman {
		t.Fatalf("expected appended root human message, got %#v", messages[1].Role)
	}
}

func TestHumanMessageNodeDoesNotCreateMissingScopeWhenPollingInput(t *testing.T) {
	t.Parallel()

	state := fruntime.State{}
	fruntime.Conversation(state, "").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeAI, "need human input"),
	})

	node := NewHumanMessageNode()
	node.StateScope = "agent"

	_, err := node.Invoke(context.Background(), state)
	if err == nil {
		t.Fatal("expected interrupt error")
	}
	if scope := state.Scope("agent"); scope != nil {
		t.Fatalf("expected missing scope to remain absent, got %#v", scope)
	}
}
