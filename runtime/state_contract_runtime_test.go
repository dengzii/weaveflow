package runtime

import (
	"context"
	"testing"

	"github.com/tmc/langchaingo/llms"
)

func TestProjectStateByContractSelectsSharedScopeAndInternalState(t *testing.T) {
	t.Parallel()

	full := State{
		"topic": "weather",
		"planner": map[string]any{
			"status": "ready",
		},
		"secret": "hidden",
	}
	full.Conversation("agent").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "hello"),
	})
	full.EnsureNamespace("__wf_iterator")["loop"] = map[string]any{
		"done": false,
	}

	projected := ProjectStateByContract(full, NodeIOContract{
		ReadPaths: []string{
			"shared.planner",
			"scopes.agent.messages",
			"internal.__wf_iterator.loop",
		},
	})

	if _, ok := projected["secret"]; ok {
		t.Fatalf("unexpected secret field leaked into projection: %#v", projected)
	}
	if planner, ok := projected["planner"].(map[string]any); !ok || planner["status"] != "ready" {
		t.Fatalf("expected planner subtree to be projected, got %#v", projected["planner"])
	}
	messages := projected.Conversation("agent").Messages()
	if len(messages) != 1 || messages[0].Role != llms.ChatMessageTypeHuman {
		t.Fatalf("expected scoped conversation messages, got %#v", messages)
	}
	internal := projected.Namespace("__wf_iterator")
	if internal == nil {
		t.Fatal("expected iterator internal namespace")
	}
	if loop, ok := internal["loop"].(map[string]any); !ok || loop["done"] != false {
		t.Fatalf("expected iterator loop state, got %#v", internal["loop"])
	}
}

func TestProjectStateByContractWildcardClonesFullState(t *testing.T) {
	t.Parallel()

	full := State{"topic": "weather"}
	projected := ProjectStateByContract(full, NodeIOContract{Wildcard: true})
	projected["topic"] = "changed"

	if full["topic"] != "weather" {
		t.Fatalf("wildcard projection must clone state, got %#v", full["topic"])
	}
}

func TestMergePatchByContractMergesAllowedWrites(t *testing.T) {
	t.Parallel()

	full := State{
		"planner": map[string]any{
			"status": "draft",
		},
	}
	patch := State{
		"planner": map[string]any{
			"status": "done",
		},
	}

	merged, err := MergePatchByContract(full, patch, NodeIOContract{
		WritePaths:         []string{"shared.planner"},
		RequiredWritePaths: []string{"shared.planner.status"},
	})
	if err != nil {
		t.Fatalf("merge patch: %v", err)
	}

	planner, ok := merged["planner"].(State)
	if !ok {
		if typed, ok := merged["planner"].(map[string]any); ok {
			planner = typed
		} else {
			t.Fatalf("expected merged planner state, got %#v", merged["planner"])
		}
	}
	if planner["status"] != "done" {
		t.Fatalf("expected merged planner status, got %#v", merged["planner"])
	}
}

func TestMergePatchByContractRejectsUndeclaredWrite(t *testing.T) {
	t.Parallel()

	_, err := MergePatchByContract(State{}, State{
		"secret": "leak",
	}, NodeIOContract{
		WritePaths: []string{"shared.output"},
	})
	if err == nil {
		t.Fatal("expected undeclared write error")
	}
}

func TestMergePatchByContractRejectsMissingRequiredWrite(t *testing.T) {
	t.Parallel()

	_, err := MergePatchByContract(State{
		"planner": map[string]any{
			"status": "draft",
		},
	}, State{
		"planner": map[string]any{
			"summary": "updated",
		},
	}, NodeIOContract{
		WritePaths:         []string{"shared.planner"},
		RequiredWritePaths: []string{"shared.planner.status"},
	})
	if err == nil {
		t.Fatal("expected missing required write error")
	}
}

func TestLegacyNodeExecutorReturnsPatch(t *testing.T) {
	t.Parallel()

	input := State{
		"topic": "old",
		"keep":  "same",
	}
	executor := LegacyNodeExecutor{
		Invoke: func(ctx context.Context, state State) (State, error) {
			_ = ctx
			delete(state, "topic")
			state["answer"] = "new"
			return state, nil
		},
	}

	patch, err := executor.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("execute legacy node: %v", err)
	}
	if _, ok := patch["keep"]; ok {
		t.Fatalf("expected untouched fields to be omitted from patch, got %#v", patch)
	}
	if value, ok := patch["topic"]; !ok || value != nil {
		t.Fatalf("expected deleted topic to be represented in patch, got %#v", patch["topic"])
	}
	if patch["answer"] != "new" {
		t.Fatalf("expected new answer patch, got %#v", patch["answer"])
	}
}
