package state

import (
	"testing"

	"github.com/tmc/langchaingo/llms"
)

func TestJSONStateCodecRoundTripsEnvelopeAndConversationMessages(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	access := NewEditingAccess(registry, NewState()).WithScope("agent")
	if err := access.SetAny(Shared("request", "input"), "hello"); err != nil {
		t.Fatalf("set request: %v", err)
	}
	if err := access.SetAny(Scope("agent", "conversation", "messages"), []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "hi"),
		llms.TextParts(llms.ChatMessageTypeAI, "done"),
	}); err != nil {
		t.Fatalf("set messages: %v", err)
	}
	if err := access.SetAny(Scope("agent", "conversation", "iteration_count"), 2); err != nil {
		t.Fatalf("set iteration: %v", err)
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
	restored, err := StateFromSnapshot(decoded)
	if err != nil {
		t.Fatalf("state from snapshot: %v", err)
	}

	restoredAccess := NewAccess(registry, restored).WithScope("agent")
	input, ok := restoredAccess.ReadAny(Shared("request", "input"))
	if !ok || input != "hello" {
		t.Fatalf("unexpected request input %#v ok=%v", input, ok)
	}
	messagesValue, ok := restoredAccess.ReadAny(Scope("agent", "conversation", "messages"))
	if !ok {
		t.Fatal("expected restored messages")
	}
	messages, ok := messagesValue.([]llms.MessageContent)
	if !ok {
		t.Fatalf("expected []llms.MessageContent, got %T", messagesValue)
	}
	if len(messages) != 2 || messages[0].Role != llms.ChatMessageTypeHuman || messages[1].Role != llms.ChatMessageTypeAI {
		t.Fatalf("unexpected messages %#v", messages)
	}
	iteration, ok := restoredAccess.ReadAny(Scope("agent", "conversation", "iteration_count"))
	if !ok || iteration != 2 {
		t.Fatalf("expected int iteration 2, got %#v ok=%v", iteration, ok)
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
