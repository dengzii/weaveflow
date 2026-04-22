package nodes

import (
	"context"
	"testing"
	fruntime "weaveflow/runtime"

	"github.com/tmc/langchaingo/llms"
)

func TestSessionBootstrapNodeInitializesEmptyScopedState(t *testing.T) {
	t.Parallel()

	node := NewSessionBootstrapNode()
	node.StateScope = "agent"
	node.Input = "Summarize the repository status."
	node.SystemPrompt = "You are a concise engineering agent."
	node.MaxIterations = 4
	node.AgentProfile = map[string]any{
		"name": "falcon",
		"mode": "general",
	}
	node.RequestMetadata = map[string]any{
		"tenant_id": "tenant-1",
		"user_id":   "user-1",
	}
	node.ToolPolicy = map[string]any{
		"allowed_tools": []any{"calculator", "current_time"},
	}

	state, err := node.Invoke(context.Background(), nil)
	if err != nil {
		t.Fatalf("invoke session bootstrap: %v", err)
	}
	if state == nil {
		t.Fatal("expected initialized state")
	}

	conversation := fruntime.Conversation(state, "agent")
	messages := conversation.Messages()
	if len(messages) != 2 {
		t.Fatalf("expected system and human messages, got %#v", messages)
	}
	if messages[0].Role != llms.ChatMessageTypeSystem || extractText(messages[0]) != "You are a concise engineering agent." {
		t.Fatalf("unexpected system message: %#v", messages[0])
	}
	if messages[1].Role != llms.ChatMessageTypeHuman || extractText(messages[1]) != "Summarize the repository status." {
		t.Fatalf("unexpected human message: %#v", messages[1])
	}
	if got := conversation.MaxIterations(); got != 4 {
		t.Fatalf("expected max iterations 4, got %d", got)
	}

	request := fruntime.Request(state)
	if request == nil || request["input"] != "Summarize the repository status." {
		t.Fatalf("expected normalized request input, got %#v", request)
	}
	metadata, ok := request["metadata"].(map[string]any)
	if !ok || metadata["tenant_id"] != "tenant-1" || metadata["user_id"] != "user-1" {
		t.Fatalf("unexpected request metadata: %#v", request["metadata"])
	}

	agent := fruntime.Agent(state)
	profile, ok := agent["profile"].(map[string]any)
	if !ok || profile["name"] != "falcon" || profile["mode"] != "general" {
		t.Fatalf("unexpected agent profile: %#v", agent["profile"])
	}

	toolPolicy := fruntime.ToolPolicy(state)
	allowed, ok := toolPolicy["allowed_tools"].([]any)
	if !ok || len(allowed) != 2 || allowed[0] != "calculator" {
		t.Fatalf("unexpected tool policy: %#v", toolPolicy)
	}
}

func TestSessionBootstrapNodeUsesConfiguredInputPath(t *testing.T) {
	t.Parallel()

	state := fruntime.State{
		"incoming": map[string]any{
			"text": "Use the local calculator.",
		},
	}

	node := NewSessionBootstrapNode()
	node.StateScope = "agent"
	node.InputPath = "incoming.text"

	_, err := node.Invoke(context.Background(), state)
	if err != nil {
		t.Fatalf("invoke session bootstrap: %v", err)
	}

	if got := fruntime.Request(state)["input"]; got != "Use the local calculator." {
		t.Fatalf("expected input path value to become request input, got %#v", got)
	}
	messages := fruntime.Conversation(state, "agent").Messages()
	if len(messages) != 1 || messages[0].Role != llms.ChatMessageTypeHuman || extractText(messages[0]) != "Use the local calculator." {
		t.Fatalf("unexpected conversation messages: %#v", messages)
	}
}

func TestSessionBootstrapNodePreservesExistingScopedConversation(t *testing.T) {
	t.Parallel()

	state := fruntime.State{}
	fruntime.Conversation(state, "agent").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "Existing input"),
	})

	node := NewSessionBootstrapNode()
	node.StateScope = "agent"
	node.Input = "New input"
	node.SystemPrompt = "Do not duplicate"
	node.MaxIterations = 2

	_, err := node.Invoke(context.Background(), state)
	if err != nil {
		t.Fatalf("invoke session bootstrap: %v", err)
	}

	messages := fruntime.Conversation(state, "agent").Messages()
	if len(messages) != 1 || extractText(messages[0]) != "Existing input" {
		t.Fatalf("expected existing conversation to be preserved, got %#v", messages)
	}
	if got := fruntime.Request(state)["input"]; got != "New input" {
		t.Fatalf("expected request input to still be normalized, got %#v", got)
	}
	if got := fruntime.Conversation(state, "agent").MaxIterations(); got != 2 {
		t.Fatalf("expected max iterations 2, got %d", got)
	}
}

func TestSessionBootstrapNodeReturnsErrorForMissingExplicitInputPath(t *testing.T) {
	t.Parallel()

	node := NewSessionBootstrapNode()
	node.InputPath = "missing.input"

	_, err := node.Invoke(context.Background(), fruntime.State{})
	if err == nil {
		t.Fatal("expected missing input path error")
	}
}
