package runtime

import (
	"strings"
	"testing"

	"github.com/tmc/langchaingo/llms"
)

func TestCloneStatePreservesRootValueWhenScopeNamesCollide(t *testing.T) {
	t.Parallel()

	state := State{
		"agent": map[string]any{
			"kind": "root",
		},
	}
	scope := state.EnsureScope("agent")
	scope["kind"] = "scope"

	cloned := state.CloneState()

	rootValue, ok := cloned["agent"].(map[string]any)
	if !ok {
		t.Fatalf("expected cloned root value to remain present, got %#v", cloned["agent"])
	}
	if rootValue["kind"] != "root" {
		t.Fatalf("expected cloned root value to be preserved, got %#v", rootValue)
	}
	if clonedScope := cloned.Scope("agent"); clonedScope == nil || clonedScope["kind"] != "scope" {
		t.Fatalf("expected cloned scope value to be preserved, got %#v", clonedScope)
	}
}

func TestStateSnapshotRoundTripPreservesRootValueAndSupportedContainers(t *testing.T) {
	t.Parallel()

	state := State{
		"agent": map[string]any{
			"kind": "root",
		},
		"tags": []string{"alpha", "beta"},
		"items": []map[string]any{
			{"name": "one"},
			{"name": "two"},
		},
	}
	scope := state.EnsureScope("agent")
	scope["kind"] = "scope"

	snapshot, err := SnapshotFromState(state)
	if err != nil {
		t.Fatalf("snapshot state: %v", err)
	}
	restored, err := StateFromSnapshot(snapshot)
	if err != nil {
		t.Fatalf("restore state: %v", err)
	}

	rootValue, ok := restored["agent"].(map[string]any)
	if !ok || rootValue["kind"] != "root" {
		t.Fatalf("expected restored root value to survive round-trip, got %#v", restored["agent"])
	}

	tags, ok := restored["tags"].([]string)
	if !ok || len(tags) != 2 || tags[0] != "alpha" || tags[1] != "beta" {
		t.Fatalf("expected restored tags to remain []string, got %#v", restored["tags"])
	}

	items, ok := restored["items"].([]map[string]any)
	if !ok || len(items) != 2 || items[0]["name"] != "one" || items[1]["name"] != "two" {
		t.Fatalf("expected restored items to remain []map[string]any, got %#v", restored["items"])
	}

	if restoredScope := restored.Scope("agent"); restoredScope == nil || restoredScope["kind"] != "scope" {
		t.Fatalf("expected restored scope value to survive round-trip, got %#v", restoredScope)
	}
}

func TestSnapshotFromStateRejectsUnsupportedValueTypes(t *testing.T) {
	t.Parallel()

	type unsupported struct {
		Label string `json:"label"`
	}

	_, err := SnapshotFromState(State{
		"bad": unsupported{Label: "x"},
	})
	if err == nil {
		t.Fatal("expected unsupported state value error")
	}
	if !strings.Contains(err.Error(), `unsupported state value at "bad"`) {
		t.Fatalf("expected path in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "runtime.unsupported") {
		t.Fatalf("expected type name in error, got %v", err)
	}
}

func TestNormalizeInputStateHandlesConversationExtensionFields(t *testing.T) {
	t.Parallel()

	normalized, err := NormalizeInputState(State{
		StateKeyMessages: []map[string]any{
			{"role": "user", "content": "hello"},
		},
		StateKeyMaxIterations: 3,
		"topic":               "demo",
	})
	if err != nil {
		t.Fatalf("normalize input state: %v", err)
	}

	conversation := Conversation(normalized, "")
	messages := conversation.Messages()
	if len(messages) != 1 {
		t.Fatalf("expected one conversation message, got %#v", messages)
	}
	if messages[0].Role != llms.ChatMessageTypeHuman {
		t.Fatalf("expected normalized conversation role %q, got %#v", llms.ChatMessageTypeHuman, messages[0].Role)
	}
	if got := conversation.MaxIterations(); got != 3 {
		t.Fatalf("expected max iterations 3, got %d", got)
	}
	if normalized["topic"] != "demo" {
		t.Fatalf("expected shared state to remain intact, got %#v", normalized)
	}
}

func TestNormalizeInputStateRejectsReservedNamespaceKeys(t *testing.T) {
	t.Parallel()

	_, err := NormalizeInputState(State{
		NormalizeStateNamespace("iterator"): map[string]any{
			"loop": map[string]any{"done": false},
		},
	})
	if err == nil {
		t.Fatal("expected reserved namespace input error")
	}
	if !strings.Contains(err.Error(), `reserved`) {
		t.Fatalf("expected reserved namespace error, got %v", err)
	}
}

func TestStateSnapshotRoundTripStoresInternalNamespacesSeparately(t *testing.T) {
	t.Parallel()

	state := State{
		"topic": "demo",
	}
	namespace := state.EnsureNamespace("iterator")
	namespace["loop"] = map[string]any{
		"next_index": 2,
		"done":       false,
	}

	snapshot, err := SnapshotFromState(state)
	if err != nil {
		t.Fatalf("snapshot state: %v", err)
	}

	internalKey := NormalizeStateNamespace("iterator")
	if _, ok := snapshot.Shared[internalKey]; ok {
		t.Fatalf("expected internal namespace %q to be excluded from shared snapshot", internalKey)
	}
	if snapshot.Internal == nil {
		t.Fatal("expected internal namespaces to be present in snapshot")
	}
	if _, ok := snapshot.Internal[internalKey]; !ok {
		t.Fatalf("expected internal namespace %q in snapshot, got %#v", internalKey, snapshot.Internal)
	}

	restored, err := StateFromSnapshot(snapshot)
	if err != nil {
		t.Fatalf("restore state: %v", err)
	}
	restoredNamespace := restored.Namespace("iterator")
	if restoredNamespace == nil {
		t.Fatal("expected internal namespace to be restored")
	}
	loopState, ok := restoredNamespace["loop"].(map[string]any)
	if !ok {
		if typed, ok := restoredNamespace["loop"].(State); ok {
			loopState = typed
		} else {
			t.Fatalf("expected restored loop state map, got %#v", restoredNamespace["loop"])
		}
	}
	if got := loopState["next_index"]; got != 2 {
		t.Fatalf("expected restored next_index 2, got %#v", got)
	}
}

func TestEnsurePlannerCreatesAndReusesPlannerState(t *testing.T) {
	t.Parallel()

	state := State{}
	planner := state.Ensure(StateKeyPlanner)
	if planner == nil {
		t.Fatal("expected planner state to be created")
	}

	planner["objective"] = "Decompose a generic task into executable steps."

	if got := state.Get(StateKeyPlanner); got == nil || got["objective"] != planner["objective"] {
		t.Fatalf("expected planner state to be readable from root state, got %#v", got)
	}

	plannerAgain := state.Ensure(StateKeyPlanner)
	if plannerAgain == nil || plannerAgain["objective"] != planner["objective"] {
		t.Fatalf("expected ensure planner to reuse the existing state, got %#v", plannerAgain)
	}
}
