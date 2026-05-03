package neo

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	fruntime "weaveflow/runtime"

	"github.com/tmc/langchaingo/llms"
)

func TestStoreConfigRoundTrip(t *testing.T) {
	store := newTestStore(t)

	want := PersistedConfig{
		SystemPrompt:      "system prompt",
		MaxIterations:     9,
		PlannerMaxSteps:   4,
		MemoryRecallLimit: 3,
		ToolFlags: map[string]bool{
			"calculator": false,
			"web_fetch":  true,
		},
		Mode: "planner",
	}
	if err := store.SaveConfig(want); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	got, ok, err := store.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if !ok {
		t.Fatal("LoadConfig() ok = false, want true")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadConfig() = %#v, want %#v", got, want)
	}
}

func TestStoreTurnWriterPersistsAssistantTimeline(t *testing.T) {
	store := newTestStore(t)

	writer, err := store.BeginTurn(defaultSessionID, "what is 2+2?")
	if err != nil {
		t.Fatalf("BeginTurn() error = %v", err)
	}

	events := []fruntime.Event{
		{Type: fruntime.EventNodeStarted, NodeID: "Planner_test"},
		{Type: fruntime.EventLLMReasoning, Payload: rawJSON(map[string]string{"text": "need calculation"})},
		{Type: fruntime.EventToolCalled, Payload: rawJSON(map[string]string{
			"tool_call_id": "call_1",
			"name":         "calculator",
			"arguments":    `{"expression":"2+2"}`,
		})},
		{Type: fruntime.EventToolReturned, Payload: rawJSON(map[string]string{
			"tool_call_id": "call_1",
			"name":         "calculator",
			"content":      "4",
		})},
		{Type: fruntime.EventLLMContent, NodeID: "Finalizer_test", Payload: rawJSONString("answer is 4")},
	}
	for _, event := range events {
		if err := writer.Publish(context.Background(), event); err != nil {
			t.Fatalf("Publish(%s) error = %v", event.Type, err)
		}
	}
	if err := writer.Finalize("completed"); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}

	history, err := store.LoadHistory(defaultSessionID)
	if err != nil {
		t.Fatalf("LoadHistory() error = %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("LoadHistory() len = %d, want 2", len(history))
	}

	userMsg := history[0]
	if userMsg.Role != string(llms.ChatMessageTypeHuman) {
		t.Fatalf("user role = %q, want %q", userMsg.Role, llms.ChatMessageTypeHuman)
	}
	if len(userMsg.Parts) != 1 || userMsg.Parts[0].Text != "what is 2+2?" {
		t.Fatalf("user parts = %#v", userMsg.Parts)
	}

	assistantMsg := history[1]
	if assistantMsg.Role != string(llms.ChatMessageTypeAI) {
		t.Fatalf("assistant role = %q, want %q", assistantMsg.Role, llms.ChatMessageTypeAI)
	}
	if assistantMsg.Status != "completed" {
		t.Fatalf("assistant status = %q, want completed", assistantMsg.Status)
	}

	wantTypes := []string{"step", "thinking", "tool_call", "tool_result", "text"}
	gotTypes := make([]string, 0, len(assistantMsg.Parts))
	for _, part := range assistantMsg.Parts {
		gotTypes = append(gotTypes, part.Type)
	}
	if !reflect.DeepEqual(gotTypes, wantTypes) {
		t.Fatalf("assistant part types = %#v, want %#v", gotTypes, wantTypes)
	}
	if assistantMsg.Parts[0].Text == "" {
		t.Fatal("step part text is empty")
	}
	if assistantMsg.Parts[2].Name != "calculator" || assistantMsg.Parts[2].Text != `{"expression":"2+2"}` {
		t.Fatalf("tool call part = %#v", assistantMsg.Parts[2])
	}
	if assistantMsg.Parts[3].Result != "4" {
		t.Fatalf("tool result part = %#v", assistantMsg.Parts[3])
	}
	if assistantMsg.Parts[4].Text != "answer is 4" {
		t.Fatalf("assistant text part = %#v", assistantMsg.Parts[4])
	}
}

func TestStoreTurnWriterIgnoresRouterContentAndPersistsFinalizerAnswer(t *testing.T) {
	store := newTestStore(t)

	writer, err := store.BeginTurn(defaultSessionID, "hi")
	if err != nil {
		t.Fatalf("BeginTurn() error = %v", err)
	}

	events := []fruntime.Event{
		{
			Type:    fruntime.EventLLMContent,
			NodeID:  "OrchestrationRouter_test",
			Payload: rawJSON(map[string]string{"text": `{"mode":"direct","direct_answer":"Hi there!"}`}),
		},
		{
			Type:    fruntime.EventLLMContent,
			NodeID:  "Finalizer_test",
			Payload: rawJSON(map[string]string{"text": "Hi there!"}),
		},
	}
	for _, event := range events {
		if err := writer.Publish(context.Background(), event); err != nil {
			t.Fatalf("Publish(%s) error = %v", event.Type, err)
		}
	}
	if err := writer.Finalize("completed"); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}

	history, err := store.LoadHistory(defaultSessionID)
	if err != nil {
		t.Fatalf("LoadHistory() error = %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("LoadHistory() len = %d, want 2", len(history))
	}

	assistantMsg := history[1]
	if len(assistantMsg.Parts) != 1 {
		t.Fatalf("assistant parts len = %d, want 1", len(assistantMsg.Parts))
	}
	if assistantMsg.Parts[0].Type != "text" || assistantMsg.Parts[0].Text != "Hi there!" {
		t.Fatalf("assistant part = %#v", assistantMsg.Parts[0])
	}
}

func TestStoreTurnWriterAppendAssistantTextPersistsFallbackAnswer(t *testing.T) {
	store := newTestStore(t)

	writer, err := store.BeginTurn(defaultSessionID, "hi")
	if err != nil {
		t.Fatalf("BeginTurn() error = %v", err)
	}
	if err := writer.AppendAssistantText("Hi there!"); err != nil {
		t.Fatalf("AppendAssistantText() error = %v", err)
	}
	if err := writer.Finalize("completed"); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}

	history, err := store.LoadHistory(defaultSessionID)
	if err != nil {
		t.Fatalf("LoadHistory() error = %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("LoadHistory() len = %d, want 2", len(history))
	}

	assistantMsg := history[1]
	if len(assistantMsg.Parts) != 1 {
		t.Fatalf("assistant parts len = %d, want 1", len(assistantMsg.Parts))
	}
	if assistantMsg.Parts[0].Text != "Hi there!" {
		t.Fatalf("assistant part = %#v", assistantMsg.Parts[0])
	}
}

func TestStoreRawHistoryRoundTripToLLM(t *testing.T) {
	store := newTestStore(t)

	history := []HistoryMessage{
		{
			Role: string(llms.ChatMessageTypeHuman),
			Parts: []MessagePart{
				{Type: "text", Text: "hello"},
			},
		},
		{
			Role: string(llms.ChatMessageTypeAI),
			Parts: []MessagePart{
				{Type: "thinking", Text: "hidden"},
				{Type: "text", Text: "world"},
			},
			Status: "completed",
		},
	}
	if err := store.SaveRawHistory(defaultSessionID, history, "completed"); err != nil {
		t.Fatalf("SaveRawHistory() error = %v", err)
	}

	msgs, err := store.LoadLLMMessages(defaultSessionID)
	if err != nil {
		t.Fatalf("LoadLLMMessages() error = %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("LoadLLMMessages() len = %d, want 2", len(msgs))
	}
	if msgs[0].Role != llms.ChatMessageTypeHuman || msgs[1].Role != llms.ChatMessageTypeAI {
		t.Fatalf("roles = [%q, %q]", msgs[0].Role, msgs[1].Role)
	}
	if len(msgs[1].Parts) != 1 {
		t.Fatalf("assistant llm parts len = %d, want 1", len(msgs[1].Parts))
	}
	textPart, ok := msgs[1].Parts[0].(llms.TextContent)
	if !ok {
		t.Fatalf("assistant llm part type = %T, want llms.TextContent", msgs[1].Parts[0])
	}
	if textPart.Text != "world" {
		t.Fatalf("assistant llm text = %q, want world", textPart.Text)
	}
}

func TestBeginTurnBootstrapsExistingRawHistory(t *testing.T) {
	store := newTestStore(t)

	rawHistory := []HistoryMessage{
		{
			Role:  string(llms.ChatMessageTypeHuman),
			Parts: []MessagePart{{Type: "text", Text: "old user"}},
		},
		{
			Role:  string(llms.ChatMessageTypeAI),
			Parts: []MessagePart{{Type: "text", Text: "old assistant"}},
		},
	}
	if err := store.SaveRawHistory(defaultSessionID, rawHistory, "completed"); err != nil {
		t.Fatalf("SaveRawHistory() error = %v", err)
	}

	writer, err := store.BeginTurn(defaultSessionID, "new user")
	if err != nil {
		t.Fatalf("BeginTurn() error = %v", err)
	}
	if err := writer.Finalize("stopped"); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}

	history, err := store.LoadHistory(defaultSessionID)
	if err != nil {
		t.Fatalf("LoadHistory() error = %v", err)
	}
	if len(history) != 4 {
		t.Fatalf("LoadHistory() len = %d, want 4", len(history))
	}
	if history[0].Parts[0].Text != "old user" || history[1].Parts[0].Text != "old assistant" {
		t.Fatalf("bootstrapped history = %#v", history[:2])
	}
	if history[2].Parts[0].Text != "new user" {
		t.Fatalf("new user history = %#v", history[2])
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()

	store, err := NewStore(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func rawJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

func rawJSONString(v string) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}
