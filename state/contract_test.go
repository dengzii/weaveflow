package state

import (
	"context"
	"testing"

	"github.com/tmc/langchaingo/llms"
)

func TestProjectStateByContractSelectsSharedScopeRuntimeAndInternalState(t *testing.T) {
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
	full.EnsureNamespace("runtime")["loop"] = map[string]any{
		"done": false,
	}
	full.EnsureNamespace("__wf_secret")["flag"] = true

	projected := ProjectStateByContract(full, NodeIOContract{
		ReadPaths: []string{
			"shared.planner",
			"scopes.agent.messages",
			"runtime.loop",
			"internal.__wf_secret.flag",
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
	runtimeState := projected.Namespace("runtime")
	if runtimeState == nil {
		t.Fatal("expected runtime namespace")
	}
	if loop, ok := runtimeState["loop"].(map[string]any); !ok || loop["done"] != false {
		t.Fatalf("expected iterator loop state, got %#v", runtimeState["loop"])
	}
	internal := projected.Namespace("__wf_secret")
	if internal == nil || internal["flag"] != true {
		t.Fatalf("expected internal namespace field, got %#v", internal)
	}
}

func TestProjectStateByContractWildcardReadClonesFullState(t *testing.T) {
	t.Parallel()

	full := State{"topic": "weather"}
	projected := ProjectStateByContract(full, NodeIOContract{WildcardRead: true})
	projected["topic"] = "changed"

	if full["topic"] != "weather" {
		t.Fatalf("wildcard projection must clone state, got %#v", full["topic"])
	}
}

func TestProjectStateByContractPreservesRootConversationFallbackForScopedMessages(t *testing.T) {
	t.Parallel()

	full := State{}
	full.Conversation("").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "hello from root"),
	})
	full.Conversation("").SetMaxIterations(16)

	projected := ProjectStateByContract(full, NodeIOContract{
		ReadPaths: []string{
			"scopes.agent.messages",
			"scopes.agent.max_iterations",
		},
	})

	messages := projected.Conversation("agent").Messages()
	if len(messages) != 1 {
		t.Fatalf("expected one projected message, got %#v", messages)
	}
	textPart, ok := messages[0].Parts[0].(llms.TextContent)
	if !ok || textPart.Text != "hello from root" {
		t.Fatalf("expected scoped projection to preserve root message fallback, got %#v", messages)
	}
	if got := projected.Conversation("agent").MaxIterations(); got != 16 {
		t.Fatalf("expected scoped projection to preserve root max_iterations fallback, got %d", got)
	}
}

func TestMergePatchByContractWildcardWriteAllowsAnyWrite(t *testing.T) {
	t.Parallel()

	merged, err := MergePatchByContract(State{}, State{
		"secret": "allowed",
	}, NodeIOContract{
		WildcardWrite: true,
	})
	if err != nil {
		t.Fatalf("merge patch with wildcard write: %v", err)
	}
	if got := merged["secret"]; got != "allowed" {
		t.Fatalf("expected wildcard write to merge patch, got %#v", merged)
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

func TestMergePatchByContractMergesScopedConversationWrites(t *testing.T) {
	t.Parallel()

	full := State{}
	full.Conversation("").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "hello from root"),
	})
	full.Conversation("").SetMaxIterations(16)

	patch := State{}
	patch.Conversation("agent").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "hello from root"),
		llms.TextParts(llms.ChatMessageTypeAI, "runner reply"),
	})
	patch.Conversation("agent").IncrementIteration()
	patch.Conversation("agent").SetFinalAnswer("runner reply")

	merged, err := MergePatchByContract(full, patch, NodeIOContract{
		WritePaths: []string{
			"scopes.agent.messages",
			"scopes.agent.iteration_count",
			"scopes.agent.final_answer",
		},
	})
	if err != nil {
		t.Fatalf("merge scoped conversation patch: %v", err)
	}

	messages := merged.Conversation("agent").Messages()
	if len(messages) != 2 {
		t.Fatalf("expected scoped conversation messages to merge, got %#v", messages)
	}
	last, ok := messages[1].Parts[0].(llms.TextContent)
	if !ok || last.Text != "runner reply" {
		t.Fatalf("unexpected scoped assistant reply: %#v", messages[1])
	}
	if got := merged.Conversation("agent").IterationCount(); got != 1 {
		t.Fatalf("expected scoped iteration count to merge, got %d", got)
	}
	if got := merged.Conversation("agent").FinalAnswer(); got != "runner reply" {
		t.Fatalf("expected scoped final answer to merge, got %q", got)
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
