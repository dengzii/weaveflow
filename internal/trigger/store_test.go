package trigger

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/runtime"
)

func TestFileStoreHonorsCanceledContext(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = store.Create(ctx, Trigger{ID: "canceled"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Create() error = %v, want context.Canceled", err)
	}
	if _, err := store.Get(context.Background(), "canceled"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("canceled Create() persisted a trigger: %v", err)
	}
}

func TestFileStoreUpdateAtomicallyReplacesTrigger(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item := Trigger{
		ID:          "webhook-1",
		Type:        TypeWebhook,
		Enabled:     true,
		Concurrency: ConcurrencyParallel,
		Target:      Target{GraphID: "graph-1"},
		InitialState: map[string]any{
			"shared": map[string]any{"tenant": "tenant-1"},
		},
		Webhook: &WebhookSpec{APIKey: "first"},
	}
	if err := store.Create(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	item.Name = "updated"
	item.Webhook.APIKey = "second"
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	stored, err := store.Get(context.Background(), item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "updated" || stored.Webhook == nil || stored.Webhook.APIKey != "second" {
		t.Fatalf("updated trigger = %#v", stored)
	}
	shared, ok := stored.InitialState["shared"].(map[string]any)
	if !ok || shared["tenant"] != "tenant-1" {
		t.Fatalf("stored initial state = %#v", stored.InitialState)
	}
}

func TestFileStorePersistsFiltersAndLimitsRecords(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	records := []Record{
		{
			ID: "record-1", TriggerID: "hook", TriggerType: TypeWebhook,
			Target: Target{GraphID: "graph-1"}, Status: runtime.RunStatusPending,
			TriggeredAt: startedAt, UpdatedAt: startedAt,
		},
		{
			ID: "record-2", TriggerID: "timer", TriggerType: TypeSchedule,
			Target: Target{GraphID: "graph-2"}, Status: runtime.RunStatusFailed,
			ErrorMessage: "start failed", TriggeredAt: startedAt.Add(time.Minute), UpdatedAt: startedAt.Add(time.Minute),
		},
		{
			ID: "record-3", TriggerID: "hook", TriggerType: TypeWebhook,
			Target: Target{GraphID: "graph-1"}, Status: runtime.RunStatusRunning,
			TriggeredAt: startedAt.Add(2 * time.Minute), UpdatedAt: startedAt.Add(2 * time.Minute),
		},
	}
	for _, record := range records {
		if err := store.CreateRecord(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	records[2].Status = runtime.RunStatusCompleted
	records[2].Run = &runtime.RunRecord{RunID: "run-3", Status: runtime.RunStatusCompleted}
	if err := store.UpdateRecord(context.Background(), records[2]); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	items, err := reopened.ListRecords(context.Background(), "hook", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "record-3" || items[0].Status != runtime.RunStatusCompleted {
		t.Fatalf("filtered records = %#v", items)
	}
	if items[0].Run == nil || items[0].Run.RunID != "run-3" {
		t.Fatalf("stored run = %#v", items[0].Run)
	}

	all, err := reopened.ListRecords(context.Background(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].ID != "record-3" || all[1].ID != "record-2" || all[2].ID != "record-1" {
		t.Fatalf("ordered records = %#v", all)
	}
}

func TestFileStorePersistsChatHistoryByConversationWithMillisecondIDs(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	triggeredAt := time.Date(2026, 8, 1, 8, 0, 0, 123000000, time.UTC)
	history := ChatHistory{
		TriggerID: "chat", UserID: "user/a", ConversationID: "conversation:one",
		TriggeredAt: triggeredAt, Status: runtime.RunStatusPending,
		TriggerMeta: ChatTriggerMeta{Channel: "weixin", MessageID: "message-1"}, GraphID: "graph",
		Messages: []ChatHistoryMessage{{
			Sequence: 1, Direction: ChatMessageInbound, Role: ChatMessageRoleUser, Kind: ChatMessageInput,
			MessageID: "message-1", Content: "hello", CreatedAt: triggeredAt,
		}},
	}
	first, err := store.CreateChatHistory(context.Background(), history)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateChatHistory(context.Background(), history)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != triggeredAt.UnixMilli() || second.ID != first.ID+1 {
		t.Fatalf("chat history ids = %d, %d", first.ID, second.ID)
	}

	completedAt := triggeredAt.Add(time.Second)
	first.Status = runtime.RunStatusCompleted
	first.CompletedAt = &completedAt
	first.RunID = "run-1"
	first.FinalAnswer = "world"
	first.Messages = append(first.Messages, ChatHistoryMessage{
		Sequence: 2, Direction: ChatMessageOutbound, Role: ChatMessageRoleAssistant, Kind: ChatMessageFinal,
		Content: "world", CreatedAt: completedAt,
	})
	if err := store.UpdateChatHistory(context.Background(), first); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	items, err := reopened.ListChatHistory(context.Background(), ChatHistoryFilter{
		TriggerID: "chat", UserID: "user/a", ConversationID: "conversation:one", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != second.ID || items[1].ID != first.ID || items[1].FinalAnswer != "world" || items[1].RunID != "run-1" {
		t.Fatalf("chat history = %#v", items)
	}
	historyDir := store.chatHistoryDir(history.TriggerID, history.UserID, history.ConversationID)
	if filepath.Base(historyDir) == history.ConversationID || filepath.Base(filepath.Dir(historyDir)) == history.UserID {
		t.Fatalf("unsafe identifiers were used as raw path segments: %q", historyDir)
	}
	if filepath.Base(store.chatHistoryPath(first)) != strconv.FormatInt(first.ID, 10)+".json" {
		t.Fatalf("chat history path = %q", store.chatHistoryPath(first))
	}

	other, err := reopened.ListChatHistory(context.Background(), ChatHistoryFilter{
		TriggerID: "chat", UserID: "user/a", ConversationID: "conversation:two", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("other conversation history = %#v", other)
	}
}
