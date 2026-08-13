package state

import (
	"testing"
)

func TestJSONStateCodecRoundTripsJSONShapeWithoutDomainReconstruction(t *testing.T) {
	t.Parallel()

	access := NewEditingAccess(NewState())
	if err := access.SetAny(Shared("request", "input"), "hello"); err != nil {
		t.Fatalf("set request: %v", err)
	}
	if err := access.SetAny(Scope("agent", "thread", "items"), []any{
		map[string]any{"kind": "input", "text": "hi"},
		map[string]any{"kind": "output", "text": "done"},
	}); err != nil {
		t.Fatalf("set items: %v", err)
	}
	if err := access.SetAny(Scope("agent", "thread", "attempt"), 2); err != nil {
		t.Fatalf("set attempt: %v", err)
	}

	snapshot, err := SnapshotFromState(access.State())
	if err != nil {
		t.Fatalf("snapshot from state: %v", err)
	}
	codec := NewJSONStateCodec("")
	encoded, err := codec.Encode(snapshot)
	if err != nil {
		t.Fatalf("encode snapshot: %v", err)
	}
	decoded, err := codec.Decode(encoded)
	if err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	restored, err := FromSnapshot(decoded)
	if err != nil {
		t.Fatalf("state from snapshot: %v", err)
	}

	restoredAccess := NewAccess(restored)
	input, ok := restoredAccess.ReadAny(Shared("request", "input"))
	if !ok || input != "hello" {
		t.Fatalf("unexpected request input %#v ok=%v", input, ok)
	}
	itemsValue, ok := restoredAccess.ReadAny(Scope("agent", "thread", "items"))
	if !ok {
		t.Fatal("expected restored items")
	}
	items, ok := itemsValue.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", itemsValue)
	}
	if len(items) != 2 {
		t.Fatalf("unexpected items %#v", items)
	}
	first, ok := items[0].(map[string]any)
	if !ok || first["kind"] != "input" || first["text"] != "hi" {
		t.Fatalf("expected map[string]any item, got %#v", items[0])
	}
	attempt, ok := restoredAccess.ReadAny(Scope("agent", "thread", "attempt"))
	if !ok || attempt != 2 {
		t.Fatalf("expected int attempt 2, got %#v ok=%v", attempt, ok)
	}
}

func TestJSONStateCodecRejectsMissingMismatchedAndUnknownVersions(t *testing.T) {
	t.Parallel()
	codec := NewJSONStateCodec("")
	if _, err := codec.Encode(Snapshot{}); err == nil {
		t.Fatal("Encode() accepted a snapshot without version")
	}
	if _, err := codec.Encode(Snapshot{Version: "state-v1"}); err == nil {
		t.Fatal("Encode() accepted a mismatched snapshot version")
	}
	for _, data := range []string{
		`{"shared":{}}`,
		`{"version":"state-v1","shared":{}}`,
		`{"version":"state-v2","legacy":true}`,
	} {
		if _, err := codec.Decode([]byte(data)); err == nil {
			t.Fatalf("Decode(%s) succeeded", data)
		}
	}
}

func TestDiffSnapshotsUsesCanonicalV2Paths(t *testing.T) {
	t.Parallel()

	before, err := SnapshotFromState(FromShared(map[string]any{
		"request": map[string]any{"input": "old"},
		"final":   map[string]any{"answer": "draft"},
	}))
	if err != nil {
		t.Fatalf("before snapshot: %v", err)
	}
	after, err := SnapshotFromState(FromShared(map[string]any{
		"request": map[string]any{"input": "new"},
		"final":   map[string]any{"answer": "done"},
	}))
	if err != nil {
		t.Fatalf("after snapshot: %v", err)
	}

	changes, err := DiffSnapshots(before, after)
	if err != nil {
		t.Fatalf("diff snapshots: %v", err)
	}
	if len(changes) != 2 {
		t.Fatalf("expected two changes, got %#v", changes)
	}
	if changes[0].Path != "shared.final.answer" || changes[0].Before != "draft" || changes[0].After != "done" {
		t.Fatalf("unexpected first change %#v", changes[0])
	}
	if changes[1].Path != "shared.request.input" || changes[1].Before != "old" || changes[1].After != "new" {
		t.Fatalf("unexpected second change %#v", changes[1])
	}
}
