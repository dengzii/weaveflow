package trigger

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/runtime"
)

func TestFileStoreDeleteGraphRemovesTriggerDataOnlyForTargetGraph(t *testing.T) {
	directory := t.TempDir()
	store, err := NewFileStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	items := []Trigger{
		{ID: "graph-hook", Type: TypeWebhook, Enabled: true, Target: Target{GraphID: "graph-a"}, Concurrency: ConcurrencyParallel, Webhook: &WebhookSpec{}, CreatedAt: now, UpdatedAt: now},
		{ID: "graph-chat", Type: TypeChat, Enabled: true, Target: Target{GraphID: "graph-a"}, Concurrency: ConcurrencyParallel, Chat: &ChatSpec{Channel: "http"}, CreatedAt: now, UpdatedAt: now},
		{ID: "other-hook", Type: TypeWebhook, Enabled: true, Target: Target{GraphID: "graph-b"}, Concurrency: ConcurrencyParallel, Webhook: &WebhookSpec{}, CreatedAt: now, UpdatedAt: now},
	}
	for _, item := range items {
		if err := store.Create(context.Background(), item); err != nil {
			t.Fatal(err)
		}
	}
	for _, record := range []Record{
		{ID: "graph-record", TriggerID: "graph-hook", TriggerType: TypeWebhook, Target: Target{GraphID: "graph-a"}, Status: runtime.RunStatusCompleted, TriggeredAt: now, UpdatedAt: now},
		{ID: "other-record", TriggerID: "other-hook", TriggerType: TypeWebhook, Target: Target{GraphID: "graph-b"}, Status: runtime.RunStatusCompleted, TriggeredAt: now, UpdatedAt: now},
	} {
		if err := store.CreateRecord(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	conversation, err := store.CreateChatConversation(context.Background(), ChatConversation{
		TriggerID: "graph-chat", UserID: "user", ChannelConversationID: "channel", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateChatHistory(context.Background(), ChatHistory{
		TriggerID: "graph-chat", UserID: "user", ChannelConversationID: "channel", ConversationID: conversation.ID,
		TriggeredAt: now, Status: runtime.RunStatusPending, TriggerMeta: ChatTriggerMeta{Channel: "http"}, GraphID: "graph-a",
		Messages: []ChatHistoryMessage{{Sequence: 1, Direction: ChatMessageInbound, Role: ChatMessageRoleUser, Kind: ChatMessageInput, Content: "hello", CreatedAt: now}},
	}); err != nil {
		t.Fatal(err)
	}

	deleted, err := store.DeleteGraph(context.Background(), "graph-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 2 || deleted[0].ID != "graph-chat" || deleted[1].ID != "graph-hook" {
		t.Fatalf("deleted triggers = %#v", deleted)
	}
	for _, id := range []string{"graph-hook", "graph-chat"} {
		if _, err := store.Get(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Get(%q) error = %v, want ErrNotFound", id, err)
		}
	}
	if _, err := store.Get(context.Background(), "other-hook"); err != nil {
		t.Fatalf("other graph trigger was removed: %v", err)
	}
	records, err := store.ListRecords(context.Background(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ID != "other-record" {
		t.Fatalf("remaining records = %#v", records)
	}
	if _, err := os.Stat(filepath.Join(directory, "history", chatHistoryPathSegment("graph-chat"))); !os.IsNotExist(err) {
		t.Fatalf("chat history directory still exists: %v", err)
	}
}
