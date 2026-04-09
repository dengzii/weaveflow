package runtime

import (
	"testing"

	"github.com/tmc/langchaingo/llms"
)

func TestMergeResumeInputMergesRootAndScopeState(t *testing.T) {
	t.Parallel()

	base := State{
		"topic": "original",
		"meta": map[string]any{
			"left": "keep",
		},
	}
	Conversation(base, "").SetMaxIterations(3)
	rootScope := base.EnsureScope("agent")
	rootScope["tool"] = "calculator"
	Conversation(base, "agent").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeAI, "need human input"),
	})

	input := State{
		"topic": "updated",
		"meta": map[string]any{
			"right": "merge",
		},
		"scopes": map[string]any{
			"agent": map[string]any{
				StateKeyMessages: []map[string]any{
					{"role": "assistant", "content": "need human input"},
					{"role": "user", "content": "approved"},
				},
				"flag": true,
			},
		},
		StateKeyMaxIterations: 5,
	}

	merged, err := mergeResumeInput(base, input)
	if err != nil {
		t.Fatalf("merge resume input: %v", err)
	}

	if got := merged["topic"]; got != "updated" {
		t.Fatalf("expected topic to be updated, got %#v", got)
	}
	meta, ok := merged["meta"].(map[string]any)
	if !ok {
		if typed, ok := merged["meta"].(State); ok {
			meta = typed
		} else {
			t.Fatalf("expected merged meta map, got %#v", merged["meta"])
		}
	}
	if meta["left"] != "keep" || meta["right"] != "merge" {
		t.Fatalf("expected merged meta keys, got %#v", meta)
	}
	if got := Conversation(merged, "").MaxIterations(); got != 5 {
		t.Fatalf("expected root max iterations 5, got %d", got)
	}

	scope := merged.Scope("agent")
	if scope == nil {
		t.Fatal("expected agent scope to exist")
	}
	if scope["tool"] != "calculator" {
		t.Fatalf("expected scope field to remain, got %#v", scope["tool"])
	}
	if scope["flag"] != true {
		t.Fatalf("expected merged scope flag, got %#v", scope["flag"])
	}

	messages := Conversation(merged, "agent").Messages()
	if len(messages) != 2 {
		t.Fatalf("expected two scoped messages, got %#v", messages)
	}
	if messages[1].Role != llms.ChatMessageTypeHuman {
		t.Fatalf("expected last scoped message to be human, got %#v", messages[1].Role)
	}
}

func TestMergeResumeInputRejectsInvalidScopePayload(t *testing.T) {
	t.Parallel()

	_, err := mergeResumeInput(State{}, State{
		"scopes": "bad",
	})
	if err == nil {
		t.Fatal("expected invalid scopes payload error")
	}
}

func TestMergeResumeInputRejectsReservedRootNamespaceKey(t *testing.T) {
	t.Parallel()

	_, err := mergeResumeInput(State{}, State{
		NormalizeStateNamespace("iterator"): map[string]any{
			"loop": map[string]any{"done": false},
		},
	})
	if err == nil {
		t.Fatal("expected reserved root namespace error")
	}
}

func TestMergeResumeInputRejectsReservedScopeNamespaceKey(t *testing.T) {
	t.Parallel()

	_, err := mergeResumeInput(State{}, State{
		"scopes": map[string]any{
			"agent": map[string]any{
				NormalizeStateNamespace("iterator"): map[string]any{
					"loop": map[string]any{"done": false},
				},
			},
		},
	})
	if err == nil {
		t.Fatal("expected reserved scope namespace error")
	}
}
