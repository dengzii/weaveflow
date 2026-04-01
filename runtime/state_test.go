package runtime

import (
	"strings"
	"testing"
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
	if got := conversation.MaxIterations(); got != 3 {
		t.Fatalf("expected max iterations 3, got %d", got)
	}
	if normalized["topic"] != "demo" {
		t.Fatalf("expected shared state to remain intact, got %#v", normalized)
	}
}
