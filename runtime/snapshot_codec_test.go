package runtime

import (
	"encoding/json"
	"testing"
)

func TestJsonEqualIdenticalBytes(t *testing.T) {
	t.Parallel()
	a := json.RawMessage(`{"a":1,"b":2}`)
	b := json.RawMessage(`{"a":1,"b":2}`)
	if !jsonEqual(a, b) {
		t.Fatal("identical bytes should be equal")
	}
}

func TestJsonEqualDifferentKeyOrder(t *testing.T) {
	t.Parallel()
	a := json.RawMessage(`{"a":1,"b":2}`)
	b := json.RawMessage(`{"b":2,"a":1}`)
	if !jsonEqual(a, b) {
		t.Fatal("different key order should be semantically equal")
	}
}

func TestJsonEqualDifferentValues(t *testing.T) {
	t.Parallel()
	a := json.RawMessage(`{"a":1}`)
	b := json.RawMessage(`{"a":2}`)
	if jsonEqual(a, b) {
		t.Fatal("different values should not be equal")
	}
}

func TestJsonEqualNestedObjectsDifferentKeyOrder(t *testing.T) {
	t.Parallel()
	a := json.RawMessage(`{"outer":{"x":1,"y":2},"z":3}`)
	b := json.RawMessage(`{"z":3,"outer":{"y":2,"x":1}}`)
	if !jsonEqual(a, b) {
		t.Fatal("nested objects with reordered keys should be semantically equal")
	}
}

func TestJsonEqualNilAndEmpty(t *testing.T) {
	t.Parallel()

	if !jsonEqual(nil, nil) {
		t.Fatal("nil vs nil should be equal")
	}
	if !jsonEqual(json.RawMessage{}, json.RawMessage{}) {
		t.Fatal("empty vs empty should be equal")
	}
	if !jsonEqual(nil, json.RawMessage{}) {
		t.Fatal("nil vs empty should be equal")
	}
	if jsonEqual(nil, json.RawMessage(`"x"`)) {
		t.Fatal("nil vs non-empty should not be equal")
	}
}

func TestJsonEqualWhitespace(t *testing.T) {
	t.Parallel()
	a := json.RawMessage(`{ "a" : 1 }`)
	b := json.RawMessage(`{"a":1}`)
	if !jsonEqual(a, b) {
		t.Fatal("whitespace differences should not affect equality")
	}
}

func TestJsonEqualArrayOrderMatters(t *testing.T) {
	t.Parallel()
	a := json.RawMessage(`[1,2,3]`)
	b := json.RawMessage(`[3,2,1]`)
	if jsonEqual(a, b) {
		t.Fatal("arrays with different order should not be equal")
	}

	c := json.RawMessage(`[1,2,3]`)
	d := json.RawMessage(`[1,2,3]`)
	if !jsonEqual(c, d) {
		t.Fatal("identical arrays should be equal")
	}
}

func TestJsonEqualInvalidJSON(t *testing.T) {
	t.Parallel()
	valid := json.RawMessage(`{"a":1}`)
	invalid := json.RawMessage(`{not json}`)

	if jsonEqual(valid, invalid) {
		t.Fatal("valid vs invalid JSON should not be equal")
	}
	if jsonEqual(invalid, valid) {
		t.Fatal("invalid vs valid JSON should not be equal")
	}
}

func TestDiffOmitsUnchangedPathsWithReorderedKeys(t *testing.T) {
	t.Parallel()

	codec := NewJSONStateCodec("")

	state := State{
		"config": map[string]any{
			"alpha": "one",
			"beta":  "two",
		},
	}

	before, err := SnapshotFromState(state)
	if err != nil {
		t.Fatalf("snapshot before: %v", err)
	}
	after, err := SnapshotFromState(state)
	if err != nil {
		t.Fatalf("snapshot after: %v", err)
	}

	changes, err := codec.Diff(before, after)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("expected no changes for identical state, got %d: %+v", len(changes), changes)
	}
}

func TestDiffDetectsActualChanges(t *testing.T) {
	t.Parallel()

	codec := NewJSONStateCodec("")

	stateBefore := State{
		"topic": "before",
		"count": 1,
	}
	stateAfter := State{
		"topic": "after",
		"count": 1,
	}

	before, err := SnapshotFromState(stateBefore)
	if err != nil {
		t.Fatalf("snapshot before: %v", err)
	}
	after, err := SnapshotFromState(stateAfter)
	if err != nil {
		t.Fatalf("snapshot after: %v", err)
	}

	changes, err := codec.Diff(before, after)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}

	found := false
	for _, change := range changes {
		if change.Path == "shared.topic" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected a change at shared.topic, got %+v", changes)
	}
}

func TestDiffDetectsAddedAndRemovedFields(t *testing.T) {
	t.Parallel()

	codec := NewJSONStateCodec("")

	stateBefore := State{"old_key": "value"}
	stateAfter := State{"new_key": "value"}

	before, err := SnapshotFromState(stateBefore)
	if err != nil {
		t.Fatalf("snapshot before: %v", err)
	}
	after, err := SnapshotFromState(stateAfter)
	if err != nil {
		t.Fatalf("snapshot after: %v", err)
	}

	changes, err := codec.Diff(before, after)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}

	paths := make(map[string]bool)
	for _, change := range changes {
		paths[change.Path] = true
	}
	if !paths["shared.old_key"] {
		t.Fatal("expected change for removed shared.old_key")
	}
	if !paths["shared.new_key"] {
		t.Fatal("expected change for added shared.new_key")
	}
}
