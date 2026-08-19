package trigger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	chatcap "github.com/dengzii/weaveflow/capability/chat"
	"github.com/dengzii/weaveflow/capability/conversation"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/chatchannel"
	"github.com/dengzii/weaveflow/llms"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

type chatRecordingStarter struct {
	initial *state.State
}

func (s *chatRecordingStarter) Start(ctx context.Context, initial *state.State) (runtime.RunRecord, *state.State, error) {
	s.initial = initial.Clone()
	if err := observeChatLLMEvent(ctx, runtime.EventLLMContentChunk, "step-worker", "worker", "call-worker", "ignored"); err != nil {
		return runtime.RunRecord{}, initial, err
	}
	if err := observeChatLLMEvent(ctx, runtime.EventLLMContentChunk, "step-answer", "answer", "call-answer", "dra"); err != nil {
		return runtime.RunRecord{}, initial, err
	}
	if err := observeChatLLMEvent(ctx, runtime.EventLLMContentChunk, "step-answer", "answer", "call-answer", "ft"); err != nil {
		return runtime.RunRecord{}, initial, err
	}
	if err := observeChatLLMEvent(ctx, runtime.EventLLMContent, "step-answer", "answer", "call-answer", "draft"); err != nil {
		return runtime.RunRecord{}, initial, err
	}
	if err := chatcap.EmitReply(ctx, chatcap.Reply{Kind: chatcap.ReplyMessage, Content: "side", NodeID: "notify"}); err != nil {
		return runtime.RunRecord{}, initial, err
	}
	return runtime.RunRecord{RunID: "chat-run", Status: runtime.RunStatusCompleted}, initial, nil
}

func observeChatLLMEvent(ctx context.Context, eventType runtime.EventType, stepID, nodeID, callID, text string) error {
	payload, err := json.Marshal(map[string]string{"call_id": callID, "text": text})
	if err != nil {
		return err
	}
	observer := runtime.RunnerEventObserverFromContext(ctx)
	if observer == nil {
		return errors.New("runtime event observer is unavailable")
	}
	return observer.Observe(ctx, runtime.Event{
		RunID:   "chat-run",
		StepID:  stepID,
		NodeID:  nodeID,
		Type:    eventType,
		Payload: payload,
	})
}

func messageText(message llms.MessageContent) string {
	var text string
	for _, part := range message.Parts {
		if content, ok := part.(llms.TextContent); ok {
			text += content.Text
		}
	}
	return text
}

func TestChatConversationMessagesPreservesCompletedRepliesOldestFirst(t *testing.T) {
	records := []ChatHistory{
		{
			Status:   runtime.RunStatusFailed,
			Messages: []ChatHistoryMessage{{Role: ChatMessageRoleUser, Content: "failed"}},
		},
		{
			Status: runtime.RunStatusCompleted,
			Messages: []ChatHistoryMessage{
				{Role: ChatMessageRoleUser, Content: "new question"},
				{Role: ChatMessageRoleAssistant, Content: "new answer"},
			},
		},
		{
			Status: runtime.RunStatusCompleted,
			Messages: []ChatHistoryMessage{
				{Role: ChatMessageRoleUser, Content: "old question"},
				{Role: ChatMessageRoleAssistant, Content: "first reply"},
				{Role: ChatMessageRoleAssistant, Content: "final reply"},
			},
		},
	}

	encoded, err := chatConversationMessages(records)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := conversation.DecodeMessages(encoded)
	if err != nil {
		t.Fatal(err)
	}
	wantRoles := []llms.ChatMessageType{
		llms.ChatMessageTypeHuman,
		llms.ChatMessageTypeAI,
		llms.ChatMessageTypeAI,
		llms.ChatMessageTypeHuman,
		llms.ChatMessageTypeAI,
	}
	wantText := []string{"old question", "first reply", "final reply", "new question", "new answer"}
	if len(messages) != len(wantRoles) {
		t.Fatalf("conversation messages = %#v", messages)
	}
	for index := range messages {
		if messages[index].Role != wantRoles[index] || messageText(messages[index]) != wantText[index] {
			t.Fatalf("conversation message %d = %#v", index, messages[index])
		}
	}
}

func TestServiceInvokeChatStreamsAndSendsMultipleReplies(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	starter := &chatRecordingStarter{}
	service, err := NewService(store, RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) {
		return starter, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), Trigger{
		ID:      "chat",
		Type:    TypeChat,
		Enabled: true,
		Target:  Target{GraphID: "graph"},
		Chat: &ChatSpec{
			StreamUpdates: true,
			StreamNodeIDs: []string{"answer"},
			StateBindings: &ChatStateBindings{
				Input:          "shared.request.input",
				TriggerID:      "scopes.chat.trigger_id",
				Channel:        "scopes.chat.channel",
				UserID:         "scopes.chat.user_id",
				ConversationID: "scopes.chat.conversation_id",
				MessageID:      "scopes.chat.message_id",
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	var replies []chatcap.Reply
	result, err := service.InvokeChat(context.Background(), "chat", chatcap.InboundMessage{
		ID:             "message-1",
		UserID:         "user-1",
		ConversationID: "conversation-1",
		Content:        "hello",
		Metadata:       map[string]any{"channel": "test"},
	}, chatcap.ReplySinkFunc(func(_ context.Context, reply chatcap.Reply) error {
		replies = append(replies, reply)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Run.RunID != "chat-run" || result.FinalReply != "side" || result.ConversationID == "" || result.ConversationID == "conversation-1" {
		t.Fatalf("result = %#v", result)
	}
	if len(replies) != 4 {
		t.Fatalf("replies = %#v", replies)
	}
	if replies[0].Kind != chatcap.ReplyUpdate || replies[0].Content != "dra" || replies[0].Sequence != 1 {
		t.Fatalf("update = %#v", replies[0])
	}
	if replies[1].Kind != chatcap.ReplyUpdate || replies[1].Content != "draft" || replies[1].Sequence != 2 {
		t.Fatalf("update = %#v", replies[1])
	}
	if replies[2].Kind != chatcap.ReplyMessage || replies[2].Content != "side" || replies[2].Sequence != 3 {
		t.Fatalf("message = %#v", replies[1])
	}
	if replies[3].Kind != chatcap.ReplyFinish || replies[3].Content != "" || replies[3].Sequence != 4 {
		t.Fatalf("finish = %#v", replies[3])
	}
	input, _ := state.ReadPath(starter.initial, "shared.request.input")
	triggerID, _ := state.ReadPath(starter.initial, "scopes.chat.trigger_id")
	messageID, _ := state.ReadPath(starter.initial, "scopes.chat.message_id")
	conversationID, _ := state.ReadPath(starter.initial, "scopes.chat.conversation_id")
	userID, _ := state.ReadPath(starter.initial, "scopes.chat.user_id")
	channel, _ := state.ReadPath(starter.initial, "scopes.chat.channel")
	if input != "hello" || triggerID != "chat" || messageID != "message-1" || conversationID != result.ConversationID || userID != "user-1" || channel != "http" {
		t.Fatalf("chat state = input:%#v trigger:%#v message:%#v conversation:%#v user:%#v channel:%#v", input, triggerID, messageID, conversationID, userID, channel)
	}
	for _, path := range []string{"shared.request.metadata", "shared.request.chat", "shared.trigger"} {
		if value, ok := state.ReadPath(starter.initial, path); ok {
			t.Fatalf("unconfigured chat state %q = %#v", path, value)
		}
	}
	records, err := service.ListRecords(context.Background(), "chat", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Status != runtime.RunStatusCompleted {
		t.Fatalf("records = %#v", records)
	}
	history, err := service.ListChatHistory(context.Background(), ChatHistoryFilter{
		TriggerID: "chat", UserID: "user-1", ChannelConversationID: "conversation-1", ConversationID: result.ConversationID, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].FinalAnswer != "side" || history[0].GraphID != "graph" || history[0].RunID != "chat-run" {
		t.Fatalf("chat history = %#v", history)
	}
	if len(history[0].Messages) != 2 || history[0].Messages[0].Kind != ChatMessageInput || history[0].Messages[1].Content != "side" || history[0].Messages[1].Kind != ChatMessageFinal {
		t.Fatalf("chat history messages = %#v", history[0].Messages)
	}
}

type chatNoReplyStarter struct{}

func (chatNoReplyStarter) Start(_ context.Context, initial *state.State) (runtime.RunRecord, *state.State, error) {
	return runtime.RunRecord{RunID: "no-reply-run", Status: runtime.RunStatusCompleted}, initial, nil
}

func TestServiceInvokeChatRejectsCompletedRunWithoutReply(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) {
		return chatNoReplyStarter{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), Trigger{
		ID: "no-reply", Type: TypeChat, Enabled: true, Target: Target{GraphID: "graph"}, Chat: &ChatSpec{},
	}); err != nil {
		t.Fatal(err)
	}
	var replies []chatcap.Reply
	result, invokeErr := service.InvokeChat(context.Background(), "no-reply", chatcap.InboundMessage{
		ID: "message-1", UserID: "user-1", ConversationID: "conversation-1", Content: "hello",
	}, chatcap.ReplySinkFunc(func(_ context.Context, reply chatcap.Reply) error {
		replies = append(replies, reply)
		return nil
	}))
	if !errors.Is(invokeErr, ErrChatReplyMissing) {
		t.Fatalf("InvokeChat() error = %v, want ErrChatReplyMissing", invokeErr)
	}
	if result.Run.RunID != "no-reply-run" || result.FinalReply != "" {
		t.Fatalf("result = %#v", result)
	}
	if len(replies) != 1 || replies[0].Kind != chatcap.ReplyFinish || replies[0].Error != ErrChatReplyMissing.Error() || replies[0].Content != "" {
		t.Fatalf("replies = %#v", replies)
	}
	records, err := service.ListRecords(context.Background(), "no-reply", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Status != runtime.RunStatusFailed || records[0].ErrorMessage != ErrChatReplyMissing.Error() {
		t.Fatalf("records = %#v", records)
	}
	history, err := service.ListChatHistory(context.Background(), ChatHistoryFilter{
		TriggerID: "no-reply", UserID: "user-1", ChannelConversationID: "conversation-1", ConversationID: result.ConversationID, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Status != runtime.RunStatusFailed || history[0].ErrorMessage != ErrChatReplyMissing.Error() {
		t.Fatalf("history = %#v", history)
	}
}

type chatHistoryStarter struct {
	initials []*state.State
}

func (s *chatHistoryStarter) Start(ctx context.Context, initial *state.State) (runtime.RunRecord, *state.State, error) {
	s.initials = append(s.initials, initial.Clone())
	input, _ := state.ReadPath(initial, "shared.request.input")
	answer := "answer:" + fmt.Sprint(input)
	if err := chatcap.EmitReply(ctx, chatcap.Reply{Kind: chatcap.ReplyMessage, Content: answer, NodeID: "reply"}); err != nil {
		return runtime.RunRecord{}, initial, err
	}
	now := time.Date(2026, 7, 31, 9, len(s.initials), 0, 0, time.UTC)
	return runtime.RunRecord{
		RunID: fmt.Sprintf("chat-history-run-%d", len(s.initials)), Status: runtime.RunStatusCompleted,
		StartedAt: now, UpdatedAt: now, FinishedAt: &now,
	}, initial, nil
}

func TestServiceInvokeChatInjectsHistoryPerTriggerUserAndConversation(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	starter := &chatHistoryStarter{}
	service, err := NewService(store, RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) {
		return starter, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), Trigger{
		ID: "chat-a", Type: TypeChat, Enabled: true, Target: Target{GraphID: "graph"},
		Chat: &ChatSpec{
			HistoryLimit: 1,
			StateBindings: &ChatStateBindings{
				Input:          "shared.request.input",
				Conversation:   "scopes.agent.conversation",
				RawHistory:     "scopes.chat.raw_history",
				TriggerID:      "scopes.chat.trigger_id",
				Channel:        "scopes.chat.channel",
				UserID:         "scopes.chat.user_id",
				ConversationID: "scopes.chat.conversation_id",
				MessageID:      "scopes.chat.message_id",
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), Trigger{
		ID: "chat-b", Type: TypeChat, Enabled: true, Target: Target{GraphID: "graph"},
		Chat: &ChatSpec{StateBindings: &ChatStateBindings{Input: "shared.request.input"}},
	}); err != nil {
		t.Fatal(err)
	}
	sink := chatcap.ReplySinkFunc(func(context.Context, chatcap.Reply) error { return nil })
	results := make(map[string]ChatResult)
	for _, invocation := range []struct {
		triggerID string
		message   chatcap.InboundMessage
	}{
		{triggerID: "chat-a", message: chatcap.InboundMessage{ID: "message-a1", UserID: "user-a", ConversationID: "conversation-a", Content: "first"}},
		{triggerID: "chat-a", message: chatcap.InboundMessage{ID: "message-a2", UserID: "user-a", ConversationID: "conversation-a", Content: "second"}},
		{triggerID: "chat-a", message: chatcap.InboundMessage{ID: "message-a3", UserID: "user-a", ConversationID: "conversation-a", Content: "third"}},
		{triggerID: "chat-a", message: chatcap.InboundMessage{ID: "message-b1", UserID: "user-b", ConversationID: "conversation-b", Content: "other user"}},
		{triggerID: "chat-b", message: chatcap.InboundMessage{ID: "message-a4", UserID: "user-a", ConversationID: "conversation-a", Content: "other trigger"}},
		{triggerID: "chat-a", message: chatcap.InboundMessage{ID: "message-a5", UserID: "user-a", ConversationID: "conversation-b", Content: "other conversation"}},
	} {
		result, err := service.InvokeChat(context.Background(), invocation.triggerID, invocation.message, sink)
		if err != nil {
			t.Fatal(err)
		}
		results[invocation.message.ID] = result
	}

	if len(starter.initials) != 6 {
		t.Fatalf("captured states = %d", len(starter.initials))
	}
	triggerID, _ := state.ReadPath(starter.initials[1], "scopes.chat.trigger_id")
	channel, _ := state.ReadPath(starter.initials[1], "scopes.chat.channel")
	userID, _ := state.ReadPath(starter.initials[1], "scopes.chat.user_id")
	conversationID, _ := state.ReadPath(starter.initials[1], "scopes.chat.conversation_id")
	messageID, _ := state.ReadPath(starter.initials[1], "scopes.chat.message_id")
	historyValue, _ := state.ReadPath(starter.initials[1], "scopes.chat.raw_history")
	history, ok := historyValue.([]any)
	if triggerID != "chat-a" || channel != "http" || userID != "user-a" || conversationID != results["message-a2"].ConversationID || conversationID != results["message-a1"].ConversationID || messageID != "message-a2" || !ok || len(history) != 1 {
		t.Fatalf("second chat request = trigger:%#v channel:%#v user:%#v conversation:%#v message:%#v history:%#v", triggerID, channel, userID, conversationID, messageID, historyValue)
	}
	turn, ok := history[0].(map[string]any)
	if !ok || turn["final_answer"] != "answer:first" {
		t.Fatalf("history turn = %#v", history[0])
	}
	messages, ok := turn["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("history messages = %#v", turn["messages"])
	}
	message, ok := messages[0].(map[string]any)
	if !ok || message["content"] != "first" || message["message_id"] != "message-a1" {
		t.Fatalf("history message = %#v", messages[0])
	}
	conversationValue, _ := state.ReadPath(starter.initials[1], "scopes.agent.conversation.messages")
	conversationMessages, err := conversation.DecodeMessages(conversationValue)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversationMessages) != 2 || conversationMessages[0].Role != llms.ChatMessageTypeHuman ||
		conversationMessages[1].Role != llms.ChatMessageTypeAI || messageText(conversationMessages[0]) != "first" ||
		messageText(conversationMessages[1]) != "answer:first" {
		t.Fatalf("conversation messages = %#v", conversationMessages)
	}
	thirdHistoryValue, _ := state.ReadPath(starter.initials[2], "scopes.chat.raw_history")
	thirdHistory, ok := thirdHistoryValue.([]any)
	if !ok || len(thirdHistory) != 1 {
		t.Fatalf("limited third chat history = %#v", thirdHistoryValue)
	}
	thirdTurn, ok := thirdHistory[0].(map[string]any)
	if !ok || thirdTurn["final_answer"] != "answer:second" {
		t.Fatalf("limited third chat turn = %#v", thirdHistory[0])
	}
	for _, index := range []int{0, 3, 5} {
		value, _ := state.ReadPath(starter.initials[index], "scopes.chat.raw_history")
		items, ok := value.([]any)
		if !ok || len(items) != 0 {
			t.Fatalf("history for invocation %d = %#v", index, value)
		}
		conversationValue, _ := state.ReadPath(starter.initials[index], "scopes.agent.conversation.messages")
		conversationItems, ok := conversationValue.([]any)
		if !ok || conversationItems == nil || len(conversationItems) != 0 {
			t.Fatalf("conversation for invocation %d = %#v", index, conversationValue)
		}
	}
	if value, ok := state.ReadPath(starter.initials[4], "scopes.chat.raw_history"); ok {
		t.Fatalf("unconfigured history was injected for other trigger: %#v", value)
	}
	if value, ok := state.ReadPath(starter.initials[4], "scopes.agent.conversation.messages"); ok {
		t.Fatalf("unconfigured conversation was injected for other trigger: %#v", value)
	}
	for index, initial := range starter.initials {
		for _, path := range []string{"shared.request.metadata", "shared.request.chat", "shared.trigger"} {
			if value, ok := state.ReadPath(initial, path); ok {
				t.Fatalf("unconfigured chat state for invocation %d at %q = %#v", index, path, value)
			}
		}
	}

	records, err := service.ListChatHistory(context.Background(), ChatHistoryFilter{
		TriggerID: "chat-a", UserID: "user-a", ChannelConversationID: "conversation-a", ConversationID: results["message-a1"].ConversationID, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 || records[0].Messages[0].MessageID != "message-a3" ||
		records[0].FinalAnswer != "answer:third" || len(records[0].Messages) != 2 {
		t.Fatalf("persisted chat history = %#v", records)
	}
	unconfiguredRecords, err := service.ListChatHistory(context.Background(), ChatHistoryFilter{
		TriggerID: "chat-b", UserID: "user-a", ChannelConversationID: "conversation-a", ConversationID: results["message-a4"].ConversationID, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(unconfiguredRecords) != 1 || unconfiguredRecords[0].Messages[0].MessageID != "message-a4" {
		t.Fatalf("persisted unconfigured chat history = %#v", unconfiguredRecords)
	}
}

type failingChatStarter struct{}

func (failingChatStarter) Start(ctx context.Context, initial *state.State) (runtime.RunRecord, *state.State, error) {
	now := time.Date(2026, 7, 31, 11, 0, 0, 0, time.UTC)
	if err := chatcap.EmitReply(ctx, chatcap.Reply{Kind: chatcap.ReplyUpdate, Content: "partial"}); err != nil {
		return runtime.RunRecord{}, initial, err
	}
	return runtime.RunRecord{
		RunID: "failed-chat-run", Status: runtime.RunStatusFailed, ErrorMessage: "model provider unavailable",
		StartedAt: now, UpdatedAt: now, FinishedAt: &now,
	}, initial, errors.New("run failed")
}

func TestServiceInvokeChatPersistsFailedTurnAndFinishReply(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) {
		return failingChatStarter{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), Trigger{
		ID: "failed-chat", Type: TypeChat, Enabled: true, Target: Target{GraphID: "graph"}, Chat: &ChatSpec{StreamUpdates: true},
	}); err != nil {
		t.Fatal(err)
	}
	var replies []chatcap.Reply
	result, invokeErr := service.InvokeChat(context.Background(), "failed-chat", chatcap.InboundMessage{
		ID: "message-1", UserID: "user-1", ConversationID: "conversation-1", Content: "hello",
	}, chatcap.ReplySinkFunc(func(_ context.Context, reply chatcap.Reply) error {
		replies = append(replies, reply)
		return nil
	}))
	if invokeErr == nil || invokeErr.Error() != "run failed" {
		t.Fatalf("InvokeChat() error = %v", invokeErr)
	}
	if result.FinalReply != "Run failed: model provider unavailable" {
		t.Fatalf("result = %#v", result)
	}
	if len(replies) != 2 || replies[0].Kind != chatcap.ReplyUpdate || replies[0].Content != "partial" ||
		replies[1].Kind != chatcap.ReplyFinish || replies[1].Content != "Run failed: model provider unavailable" || replies[1].Error != "" {
		t.Fatalf("failure replies = %#v", replies)
	}
	records, err := service.ListRecords(context.Background(), "failed-chat", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Status != runtime.RunStatusFailed || records[0].ErrorMessage != "run failed" ||
		records[0].Run == nil || records[0].Run.RunID != "failed-chat-run" {
		t.Fatalf("failed chat record = %#v", records)
	}
	history, err := service.ListChatHistory(context.Background(), ChatHistoryFilter{
		TriggerID: "failed-chat", UserID: "user-1", ChannelConversationID: "conversation-1", ConversationID: result.ConversationID, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Status != runtime.RunStatusFailed || history[0].RunID != "failed-chat-run" ||
		history[0].ErrorMessage != "run failed" || history[0].FinalAnswer != "Run failed: model provider unavailable" ||
		len(history[0].Messages) != 2 || history[0].Messages[1].Content != "Run failed: model provider unavailable" || history[0].Messages[1].Kind != ChatMessageFinal {
		t.Fatalf("failed chat history = %#v", history)
	}
}

func TestChatRunFinishContentDescribesTerminalStatus(t *testing.T) {
	tests := []struct {
		name   string
		run    runtime.RunRecord
		runErr error
		want   string
	}{
		{name: "failed with run reason", run: runtime.RunRecord{Status: runtime.RunStatusFailed, ErrorMessage: "node failed"}, runErr: errors.New("fallback failure"), want: "Run failed: node failed"},
		{name: "failed with returned error", run: runtime.RunRecord{Status: runtime.RunStatusFailed}, runErr: errors.New("fallback failure"), want: "Run failed: fallback failure"},
		{name: "failed without reason", run: runtime.RunRecord{Status: runtime.RunStatusFailed}, want: "Run failed."},
		{name: "canceled", run: runtime.RunRecord{Status: runtime.RunStatusCanceled}, runErr: context.Canceled, want: "Response stopped."},
		{name: "paused", run: runtime.RunRecord{Status: runtime.RunStatusPaused}, want: "Run paused."},
		{name: "completed", run: runtime.RunRecord{Status: runtime.RunStatusCompleted}, want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := chatRunFinishContent(test.run, test.runErr); got != test.want {
				t.Fatalf("chatRunFinishContent() = %q, want %q", got, test.want)
			}
		})
	}
}

type cancelableChatStarter struct {
	started chan struct{}
	once    sync.Once
}

func (s *cancelableChatStarter) Start(ctx context.Context, initial *state.State) (runtime.RunRecord, *state.State, error) {
	if err := chatcap.EmitReply(ctx, chatcap.Reply{Kind: chatcap.ReplyUpdate, Content: "working"}); err != nil {
		return runtime.RunRecord{}, initial, err
	}
	s.once.Do(func() { close(s.started) })
	<-ctx.Done()
	now := time.Now().UTC()
	return runtime.RunRecord{
		RunID: "canceled-chat-run", Status: runtime.RunStatusCanceled,
		StartedAt: now, UpdatedAt: now, FinishedAt: &now,
	}, initial, ctx.Err()
}

func TestServiceChatCommandsCreateConversationAndStopActiveRun(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	starter := &cancelableChatStarter{started: make(chan struct{})}
	service, err := NewService(store, RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) {
		return starter, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 8, 4, 12, 0, 0, 123000000, time.UTC)
	service.now = func() time.Time { return createdAt }
	if _, err := service.Create(context.Background(), Trigger{
		ID: "command-chat", Type: TypeChat, Enabled: true, Target: Target{GraphID: "graph"}, Chat: &ChatSpec{StreamUpdates: true},
	}); err != nil {
		t.Fatal(err)
	}

	invocationDone := make(chan struct{})
	var invocationResult ChatResult
	var invocationErr error
	var invocationReplies []chatcap.Reply
	go func() {
		defer close(invocationDone)
		invocationResult, invocationErr = service.InvokeChat(context.Background(), "command-chat", chatcap.InboundMessage{
			ID: "message-1", UserID: "user-1", ConversationID: "channel-thread", Content: "long task",
		}, chatcap.ReplySinkFunc(func(_ context.Context, reply chatcap.Reply) error {
			invocationReplies = append(invocationReplies, reply)
			return nil
		}))
	}()
	select {
	case <-starter.started:
	case <-time.After(time.Second):
		t.Fatal("chat invocation did not start")
	}

	var stopReplies []chatcap.Reply
	stopResult, err := service.InvokeChat(context.Background(), "command-chat", chatcap.InboundMessage{
		ID: "message-stop", UserID: "user-1", ConversationID: "channel-thread", Content: "/STOP",
	}, chatcap.ReplySinkFunc(func(_ context.Context, reply chatcap.Reply) error {
		stopReplies = append(stopReplies, reply)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if stopResult.Command != chatCommandStop || stopResult.FinalReply != "Response stopped." || len(stopReplies) != 1 || stopReplies[0].Kind != chatcap.ReplyFinish {
		t.Fatalf("stop result = %#v, replies = %#v", stopResult, stopReplies)
	}
	select {
	case <-invocationDone:
	case <-time.After(time.Second):
		t.Fatal("active chat invocation was not canceled")
	}
	if !errors.Is(invocationErr, context.Canceled) || invocationResult.ConversationID == "" ||
		stopResult.ConversationID != invocationResult.ConversationID || invocationResult.FinalReply != "Response stopped." {
		t.Fatalf("canceled invocation result = %#v, err = %v", invocationResult, invocationErr)
	}
	if len(invocationReplies) != 2 || invocationReplies[0].Kind != chatcap.ReplyUpdate || invocationReplies[0].Content != "working" ||
		invocationReplies[1].Kind != chatcap.ReplyFinish || invocationReplies[1].Content != "Response stopped." || invocationReplies[1].Error != "" {
		t.Fatalf("canceled invocation replies = %#v", invocationReplies)
	}

	newResult, err := service.InvokeChat(context.Background(), "command-chat", chatcap.InboundMessage{
		ID: "message-new", UserID: "user-1", ConversationID: "channel-thread", Content: "/new",
	}, chatcap.ReplySinkFunc(func(context.Context, chatcap.Reply) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	if newResult.Command != chatCommandNew || newResult.ConversationID == "" || newResult.ConversationID == invocationResult.ConversationID {
		t.Fatalf("new conversation result = %#v", newResult)
	}
	for _, conversationID := range []string{invocationResult.ConversationID, newResult.ConversationID} {
		dir := store.chatConversationDir("command-chat", "user-1", "channel-thread", conversationID)
		if _, err := os.Stat(filepath.Join(dir, "conversation.json")); err != nil {
			t.Fatalf("conversation directory %q is missing: %v", dir, err)
		}
	}
	current, err := store.CurrentChatConversation(context.Background(), ChatConversationIdentity{
		TriggerID: "command-chat", UserID: "user-1", ChannelConversationID: "channel-thread",
	})
	if err != nil || current.ID != newResult.ConversationID {
		t.Fatalf("current conversation = %#v, err = %v", current, err)
	}
	records, err := service.ListRecords(context.Background(), "command-chat", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Status != runtime.RunStatusCanceled {
		t.Fatalf("command trigger records = %#v", records)
	}
	history, err := service.ListChatHistory(context.Background(), ChatHistoryFilter{
		TriggerID: "command-chat", UserID: "user-1", ChannelConversationID: "channel-thread", ConversationID: invocationResult.ConversationID, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Status != runtime.RunStatusCanceled || history[0].ErrorMessage != context.Canceled.Error() ||
		history[0].FinalAnswer != "Response stopped." || len(history[0].Messages) != 2 ||
		history[0].Messages[1].Content != "Response stopped." || history[0].Messages[1].Kind != ChatMessageFinal {
		t.Fatalf("canceled chat history = %#v", history)
	}
}

func TestParseChatCommand(t *testing.T) {
	for input, expected := range map[string]string{
		" /NEW ": chatCommandNew,
		"/stop":  chatCommandStop,
		"/Help":  chatCommandHelp,
		"new":    "",
		"/new x": "",
	} {
		if actual := parseChatCommand(input); actual != expected {
			t.Fatalf("parseChatCommand(%q) = %q, want %q", input, actual, expected)
		}
	}
}

func TestChatLLMStreamObserverAccumulatesContentPerCall(t *testing.T) {
	var replies []chatcap.Reply
	observer := newChatLLMStreamObserver(chatcap.ReplySinkFunc(func(_ context.Context, reply chatcap.Reply) error {
		replies = append(replies, reply)
		return nil
	}))
	ctx := runtime.WithRunnerEventObserver(context.Background(), observer)

	for _, item := range []struct {
		typ    runtime.EventType
		callID string
		text   string
	}{
		{typ: runtime.EventLLMContentChunk, callID: "call-1", text: "hello"},
		{typ: runtime.EventLLMContentChunk, callID: "call-1", text: " "},
		{typ: runtime.EventLLMContentChunk, callID: "call-1", text: "world"},
		{typ: runtime.EventLLMContent, callID: "call-1", text: "hello world"},
		{typ: runtime.EventLLMContentChunk, callID: "call-2", text: "replacement"},
	} {
		if err := observeChatLLMEvent(ctx, item.typ, "step", "answer", item.callID, item.text); err != nil {
			t.Fatal(err)
		}
	}

	if len(replies) != 3 {
		t.Fatalf("replies = %#v", replies)
	}
	if replies[0].Content != "hello" || replies[1].Content != "hello world" || replies[2].Content != "replacement" {
		t.Fatalf("reply contents = [%q %q %q]", replies[0].Content, replies[1].Content, replies[2].Content)
	}
}

type lifecycleChannelFactory struct {
	started chan map[string]any
	stopped chan struct{}
}

func (factory *lifecycleChannelFactory) Definition() chatchannel.Definition {
	return chatchannel.Definition{
		ID:    "lifecycle",
		Title: "Lifecycle",
		ConfigSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":   map[string]any{"type": "string"},
				"secret": map[string]any{"type": "string", "writeOnly": true},
			},
			"required": []any{"name", "secret"},
		},
	}
}

func (factory *lifecycleChannelFactory) ValidateConfig(config map[string]any) error {
	if config["name"] == "" || config["secret"] == "" || config["secret"] == nil {
		return errors.New("name and secret are required")
	}
	return nil
}

func (factory *lifecycleChannelFactory) New(config chatchannel.InstanceConfig) (chatchannel.Instance, error) {
	return &lifecycleChannel{config: config.Config, started: factory.started, stopped: factory.stopped}, nil
}

type lifecycleChannel struct {
	config  map[string]any
	started chan map[string]any
	stopped chan struct{}
}

type lifecycleSecretResolver struct {
	value string
	calls int
}

func (resolver *lifecycleSecretResolver) Resolve(_ context.Context, ref dsl.SecretRef) (string, error) {
	resolver.calls++
	if ref.Source != "env" || ref.Ref != "CHAT_CHANNEL_SECRET" {
		return "", fmt.Errorf("unexpected chat channel secret ref")
	}
	return resolver.value, nil
}

func (channel *lifecycleChannel) Run(ctx context.Context) error {
	channel.started <- channel.config
	<-ctx.Done()
	channel.stopped <- struct{}{}
	return nil
}

func TestServiceManagesRegisteredChatChannelLifecycleAndSecrets(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	factory := &lifecycleChannelFactory{started: make(chan map[string]any, 2), stopped: make(chan struct{}, 2)}
	secretResolver := &lifecycleSecretResolver{value: "stored-secret"}
	channels := chatchannel.NewDefaultRegistry()
	if err := channels.Register(factory); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(
		store,
		RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) { return &recordingStarter{}, nil }),
		WithChatChannels(channels),
		WithSecretResolver(secretResolver),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), Trigger{
		ID: "plaintext-chat", Type: TypeChat, Enabled: false, Target: Target{GraphID: "graph"},
		Chat: &ChatSpec{
			Channel:       "lifecycle",
			ChannelConfig: map[string]any{"name": "plaintext", "secret": "must-not-persist"},
		},
	}); !errors.Is(err, ErrInvalidTrigger) {
		t.Fatalf("plaintext channel secret error = %v, want ErrInvalidTrigger", err)
	}
	created, err := service.Create(context.Background(), Trigger{
		ID: "managed-chat", Type: TypeChat, Enabled: true, Target: Target{GraphID: "graph"},
		Chat: &ChatSpec{
			Channel:       "lifecycle",
			ChannelConfig: map[string]any{"name": "first", "secret": dsl.SecretRef{Source: "env", Ref: "CHAT_CHANNEL_SECRET"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if redacted := service.RedactChatChannelConfig(created); redacted.Chat.ChannelConfig["secret"] != nil {
		t.Fatalf("redacted chat config = %#v", redacted.Chat.ChannelConfig)
	}
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.Close() }()
	select {
	case config := <-factory.started:
		if config["name"] != "first" || config["secret"] != "stored-secret" {
			t.Fatalf("started config = %#v", config)
		}
	case <-time.After(time.Second):
		t.Fatal("chat channel did not start")
	}

	secretResolver.value = "rotated-secret"
	updated, err := service.Update(context.Background(), Trigger{
		ID: "managed-chat", Type: TypeChat, Enabled: true, Target: Target{GraphID: "graph"},
		Chat: &ChatSpec{Channel: "lifecycle", ChannelConfig: map[string]any{"name": "second"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ref, err := chatchannel.ParseSecretRef(updated.Chat.ChannelConfig["secret"])
	if err != nil || ref.Source != "env" || ref.Ref != "CHAT_CHANNEL_SECRET" {
		t.Fatalf("updated chat config = %#v, err = %v", updated.Chat.ChannelConfig, err)
	}
	select {
	case <-factory.stopped:
	case <-time.After(time.Second):
		t.Fatal("previous chat channel did not stop")
	}
	select {
	case config := <-factory.started:
		if config["name"] != "second" || config["secret"] != "rotated-secret" {
			t.Fatalf("restarted config = %#v", config)
		}
	case <-time.After(time.Second):
		t.Fatal("updated chat channel did not start")
	}
	if err := service.Delete(context.Background(), "managed-chat"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-factory.stopped:
	case <-time.After(time.Second):
		t.Fatal("deleted chat channel did not stop")
	}
}

func TestServiceReplaceGraphIsAtomicAndUpdatesLiveChannels(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	factory := &lifecycleChannelFactory{started: make(chan map[string]any, 2), stopped: make(chan struct{}, 2)}
	secretResolver := &lifecycleSecretResolver{value: "stored-secret"}
	channels := chatchannel.NewDefaultRegistry()
	if err := channels.Register(factory); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(
		store,
		RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) { return &recordingStarter{}, nil }),
		WithChatChannels(channels),
		WithSecretResolver(secretResolver),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ReplaceGraph(context.Background(), "graph-a", []Trigger{{
		ID: "chat", Type: TypeChat, Enabled: true,
		Chat: &ChatSpec{Channel: "lifecycle", ChannelConfig: map[string]any{"name": "first", "secret": dsl.SecretRef{Source: "env", Ref: "CHAT_CHANNEL_SECRET"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReplaceGraph(context.Background(), "graph-b", []Trigger{{
		ID: "other", Type: TypeWebhook, Enabled: true, Webhook: &WebhookSpec{},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.Close() }()
	select {
	case config := <-factory.started:
		if config["name"] != "first" || config["secret"] != "stored-secret" {
			t.Fatalf("initial channel config = %#v", config)
		}
	case <-time.After(time.Second):
		t.Fatal("initial chat channel did not start")
	}

	secretResolver.value = "rotated-secret"
	replaced, err := service.ReplaceGraph(context.Background(), "graph-a", []Trigger{{
		ID: "chat", Type: TypeChat, Enabled: true,
		Chat: &ChatSpec{Channel: "lifecycle", ChannelConfig: map[string]any{"name": "first"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if ref, parseErr := chatchannel.ParseSecretRef(replaced[0].Chat.ChannelConfig["secret"]); parseErr != nil || ref.Ref != "CHAT_CHANNEL_SECRET" {
		t.Fatalf("preserved channel secret ref = %#v, err = %v", replaced[0].Chat.ChannelConfig["secret"], parseErr)
	}
	select {
	case <-factory.stopped:
	case <-time.After(time.Second):
		t.Fatal("replaced chat channel did not stop")
	}
	select {
	case config := <-factory.started:
		if config["name"] != "first" || config["secret"] != "rotated-secret" {
			t.Fatalf("replaced channel config = %#v", config)
		}
	case <-time.After(time.Second):
		t.Fatal("replacement chat channel did not start")
	}

	if _, err := service.ReplaceGraph(context.Background(), "graph-a", []Trigger{{
		ID: "other", Type: TypeWebhook, Enabled: true, Webhook: &WebhookSpec{},
	}}); !errors.Is(err, ErrExists) {
		t.Fatalf("cross-graph replacement error = %v, want ErrExists", err)
	}
	items, err := service.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("triggers after failed replacement = %#v, want two unchanged items", items)
	}

	if _, err := service.ReplaceGraph(context.Background(), "graph-a", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-factory.stopped:
	case <-time.After(time.Second):
		t.Fatal("removed chat channel did not stop")
	}
	items, err = service.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "other" || items[0].Target.GraphID != "graph-b" {
		t.Fatalf("remaining triggers = %#v, want graph-b trigger", items)
	}
}

func TestServiceInspectDefinitionsSerializesMutations(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) {
		return &recordingStarter{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}

	inspectionStarted := make(chan struct{})
	releaseInspection := make(chan struct{})
	inspectionDone := make(chan error, 1)
	go func() {
		inspectionDone <- service.InspectDefinitions(context.Background(), func([]Trigger) error {
			close(inspectionStarted)
			<-releaseInspection
			return nil
		})
	}()
	<-inspectionStarted

	mutationDone := make(chan error, 1)
	go func() {
		_, mutationErr := service.Create(context.Background(), Trigger{
			ID:      "serialized-trigger",
			Type:    TypeWebhook,
			Enabled: false,
			Target:  Target{GraphID: "graph"},
			Webhook: &WebhookSpec{},
		})
		mutationDone <- mutationErr
	}()
	select {
	case mutationErr := <-mutationDone:
		t.Fatalf("definition mutation completed during inspection: %v", mutationErr)
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseInspection)
	if err := <-inspectionDone; err != nil {
		t.Fatal(err)
	}
	if err := <-mutationDone; err != nil {
		t.Fatal(err)
	}
}

func TestServiceAllowsDisabledChatChannelWithoutCredentials(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	channels := chatchannel.NewDefaultRegistry()
	if err := channels.Register(&lifecycleChannelFactory{}); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(
		store,
		RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) { return &recordingStarter{}, nil }),
		WithChatChannels(channels),
	)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(context.Background(), Trigger{
		ID: "disabled-chat", Type: TypeChat, Enabled: false, Target: Target{GraphID: "graph"},
		Chat: &ChatSpec{Channel: "lifecycle", ChannelConfig: map[string]any{"name": "imported"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Enabled {
		t.Fatal("disabled chat trigger was enabled")
	}

	created.Enabled = true
	if _, err := service.Update(context.Background(), created); err == nil {
		t.Fatal("enabled chat trigger without credentials was accepted")
	}
}

type recordingStarter struct {
	initial *state.State
	calls   int
}

func (r *recordingStarter) Start(_ context.Context, initial *state.State) (runtime.RunRecord, *state.State, error) {
	r.initial = initial.Clone()
	r.calls++
	return runtime.RunRecord{RunID: "run-1", Status: runtime.RunStatusCompleted}, initial, nil
}

type rejectingInitialStateStarter struct {
	calls       int
	validations int
}

func (r *rejectingInitialStateStarter) ValidateInitialState(*state.State) error {
	r.validations++
	return errors.New("missing shared.required")
}

func (r *rejectingInitialStateStarter) Start(_ context.Context, initial *state.State) (runtime.RunRecord, *state.State, error) {
	r.calls++
	return runtime.RunRecord{}, initial, nil
}

func TestServiceValidatesTriggerStateBeforeStartingNormalAndChatRuns(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	starter := &rejectingInitialStateStarter{}
	service, err := NewService(store, RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) {
		return starter, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []Trigger{
		{ID: "hook", Type: TypeWebhook, Enabled: true, Target: Target{GraphID: "graph"}, Webhook: &WebhookSpec{}},
		{ID: "chat", Type: TypeChat, Enabled: true, Target: Target{GraphID: "graph"}, Chat: &ChatSpec{}},
	} {
		if _, err := service.Create(context.Background(), item); err != nil {
			t.Fatalf("create %s trigger: %v", item.Type, err)
		}
	}

	if _, err := service.InvokeWebhook(context.Background(), "hook", []byte(`{}`), nil); err == nil || !strings.Contains(err.Error(), "validate trigger initial state") {
		t.Fatalf("webhook preflight error = %v", err)
	}
	if _, err := service.InvokeChat(context.Background(), "chat", chatcap.InboundMessage{
		ID: "message", UserID: "user", ConversationID: "channel-conversation", Content: "hello",
	}, chatcap.ReplySinkFunc(func(context.Context, chatcap.Reply) error { return nil })); err == nil || !strings.Contains(err.Error(), "validate trigger initial state") {
		t.Fatalf("chat preflight error = %v", err)
	}
	if starter.validations != 2 || starter.calls != 0 {
		t.Fatalf("validations = %d, starts = %d, want 2 validations before zero starts", starter.validations, starter.calls)
	}
}

type asyncRecordingStarter struct {
	recordingStarter
	done <-chan struct{}
}

type queuedAsyncRecordingStarter struct {
	asyncRecordingStarter
	queuedCalls int
}

func (starter *queuedAsyncRecordingStarter) Enqueue(_ context.Context, initial *state.State) (runtime.RunRecord, error) {
	starter.initial = initial.Clone()
	starter.queuedCalls++
	return runtime.RunRecord{RunID: "run-queued", Status: runtime.RunStatusPending}, nil
}

func TestServicePrefersDurableEnqueue(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	starter := &queuedAsyncRecordingStarter{}
	service, err := NewService(store, RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) {
		return starter, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), Trigger{
		ID: "webhook-queued", Type: TypeWebhook, Enabled: true,
		Target: Target{GraphID: "graph-1"}, Webhook: &WebhookSpec{},
	}); err != nil {
		t.Fatal(err)
	}
	run, err := service.InvokeWebhook(context.Background(), "webhook-queued", []byte(`{}`), nil)
	if err != nil {
		t.Fatalf("InvokeWebhook() error = %v", err)
	}
	if run.RunID != "run-queued" || starter.queuedCalls != 1 || starter.calls != 0 {
		t.Fatalf("queued run = %#v, queued calls = %d, direct calls = %d", run, starter.queuedCalls, starter.calls)
	}
}

func (r *asyncRecordingStarter) StartAsync(_ context.Context, initial *state.State) (runtime.RunRecord, <-chan struct{}, error) {
	r.initial = initial.Clone()
	r.calls++
	return runtime.RunRecord{RunID: "run-async", Status: runtime.RunStatusRunning}, r.done, nil
}

func TestServiceInvokeWebhookBuildsState(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	starter := &recordingStarter{}
	service, err := NewService(store, RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) {
		return starter, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Create(context.Background(), Trigger{
		ID:      "webhook-1",
		Type:    TypeWebhook,
		Enabled: true,
		Target:  Target{GraphID: "graph-1"},
		InitialState: map[string]any{
			"shared": map[string]any{"tenant": map[string]any{"id": "tenant-1"}},
			"scopes": map[string]any{"agent": map[string]any{"mode": "review"}},
		},
		Webhook: &WebhookSpec{
			StateBindings: &WebhookStateBindings{
				Input:       "scopes.webhook.input",
				Metadata:    "scopes.webhook.metadata",
				TriggerID:   "scopes.webhook.trigger_id",
				TriggerType: "scopes.webhook.trigger_type",
			},
			StateMappings: []WebhookStateMapping{
				{Parameter: "message", StatePath: "shared.webhook.message"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"message":"hello"}`)
	run, err := service.InvokeWebhook(context.Background(), "webhook-1", body, map[string]string{
		"Authorization": "Bearer secret-token",
		"Cookie":        "session=secret",
		"X-Trace-ID":    "trace-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.RunID != "run-1" || starter.calls != 1 {
		t.Fatalf("run = %#v, calls = %d", run, starter.calls)
	}
	input, ok := state.ReadPath(starter.initial, "scopes.webhook.input")
	if !ok {
		t.Fatal("webhook input is missing")
	}
	values, ok := input.(map[string]any)
	if !ok || values["message"] != "hello" {
		t.Fatalf("webhook input = %#v", input)
	}
	mapped, ok := state.ReadPath(starter.initial, "shared.webhook.message")
	if !ok || mapped != "hello" {
		t.Fatalf("mapped webhook input = %#v", mapped)
	}
	tenantID, ok := state.ReadPath(starter.initial, "shared.tenant.id")
	if !ok || tenantID != "tenant-1" {
		t.Fatalf("trigger initial shared state = %#v", tenantID)
	}
	agentMode, ok := state.ReadPath(starter.initial, "scopes.agent.mode")
	if !ok || agentMode != "review" {
		t.Fatalf("trigger initial scoped state = %#v", agentMode)
	}
	triggerID, ok := state.ReadPath(starter.initial, "scopes.webhook.trigger_id")
	if !ok || triggerID != "webhook-1" {
		t.Fatalf("trigger id = %#v", triggerID)
	}
	triggerType, ok := state.ReadPath(starter.initial, "scopes.webhook.trigger_type")
	if !ok || triggerType != "webhook" {
		t.Fatalf("trigger type = %#v", triggerType)
	}
	metadataValue, ok := state.ReadPath(starter.initial, "scopes.webhook.metadata")
	if !ok {
		t.Fatal("webhook metadata is missing")
	}
	metadata, ok := metadataValue.(map[string]any)
	if !ok || metadata["X-Trace-ID"] != "trace-1" {
		t.Fatalf("webhook metadata = %#v", metadataValue)
	}
	for _, sensitive := range []string{"Authorization", "Cookie"} {
		if _, exists := metadata[sensitive]; exists {
			t.Fatalf("sensitive header %q leaked into metadata: %#v", sensitive, metadata)
		}
	}
}

func TestTriggerRejectsInvalidInitialState(t *testing.T) {
	tests := []map[string]any{
		{"runtime": map[string]any{"run_id": "spoofed"}},
		{"shared": "not-an-object"},
	}
	for _, initialState := range tests {
		item := Trigger{
			ID:           "webhook",
			Type:         TypeWebhook,
			Concurrency:  ConcurrencyParallel,
			Target:       Target{GraphID: "graph-1"},
			InitialState: initialState,
			Webhook:      &WebhookSpec{},
		}
		if err := item.Validate(); err == nil {
			t.Fatalf("initial state %#v was accepted", initialState)
		} else if !errors.Is(err, ErrInvalidTrigger) {
			t.Fatalf("initial state error = %v", err)
		}
	}
}

func TestTriggerProducedStateContractCoversInvocationWrites(t *testing.T) {
	tests := []struct {
		name     string
		trigger  Trigger
		expected map[string]string
	}{
		{
			name: "webhook",
			trigger: Trigger{
				Type: TypeWebhook,
				InitialState: map[string]any{"shared": map[string]any{
					"tenant": map[string]any{"id": "tenant-1"},
				}},
				Webhook: &WebhookSpec{
					StateBindings: &WebhookStateBindings{
						Input: "shared.request.input", Metadata: "shared.request.metadata",
						TriggerID: "shared.trigger.id", TriggerType: "shared.trigger.type", RawBody: "shared.request.raw",
					},
					StateMappings: []WebhookStateMapping{{Parameter: "user.id", StatePath: "scopes.hook.user_id"}},
				},
			},
			expected: map[string]string{
				"shared.tenant": "object", "shared.tenant.id": "string",
				"shared.request.input": "", "shared.request.metadata": "object", "shared.request.raw": "string",
				"shared.trigger.id": "string", "shared.trigger.type": "string", "scopes.hook.user_id": "",
			},
		},
		{
			name: "schedule",
			trigger: Trigger{Type: TypeSchedule, Schedule: &ScheduleSpec{StateBindings: &ScheduleStateBindings{
				Input: "scopes.timer.input", Metadata: "scopes.timer.metadata",
				TriggerID: "scopes.timer.trigger_id", TriggerType: "scopes.timer.trigger_type",
			}}},
			expected: map[string]string{
				"scopes.timer.input": "", "scopes.timer.metadata": "object",
				"scopes.timer.trigger_id": "string", "scopes.timer.trigger_type": "string",
			},
		},
		{
			name: "chat",
			trigger: Trigger{Type: TypeChat, Chat: &ChatSpec{StateBindings: &ChatStateBindings{
				Input: "scopes.chat.input", Conversation: "scopes.chat.conversation", RawHistory: "scopes.chat.raw_history",
				TriggerID: "scopes.chat.trigger_id", Channel: "scopes.chat.channel", UserID: "scopes.chat.user_id",
				ConversationID: "scopes.chat.conversation_id", MessageID: "scopes.chat.message_id",
			}}},
			expected: map[string]string{
				"scopes.chat.input": "string", "scopes.chat.conversation.messages": "array", "scopes.chat.raw_history": "array",
				"scopes.chat.trigger_id": "string", "scopes.chat.channel": "string", "scopes.chat.user_id": "string",
				"scopes.chat.conversation_id": "string", "scopes.chat.message_id": "string",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contract, err := test.trigger.ProducedStateContract()
			if err != nil {
				t.Fatal(err)
			}
			actual := make(map[string]string, len(contract.Fields))
			for _, field := range contract.Fields {
				if field.Mode != state.AccessWrite {
					t.Fatalf("field %q mode = %q, want write", field.Path.String(), field.Mode)
				}
				actual[field.Path.String()] = field.Type
			}
			if !reflect.DeepEqual(actual, test.expected) {
				t.Fatalf("produced fields = %#v, want %#v", actual, test.expected)
			}
		})
	}
}

func TestTriggerRejectsOverlappingStateDestinations(t *testing.T) {
	tests := []Trigger{
		{
			ID: "webhook", Type: TypeWebhook, Concurrency: ConcurrencyParallel, Target: Target{GraphID: "graph-1"},
			Webhook: &WebhookSpec{
				StateBindings: &WebhookStateBindings{Input: "shared.request.input"},
				StateMappings: []WebhookStateMapping{{Parameter: "user", StatePath: "shared.request.input.user"}},
			},
		},
		{
			ID: "schedule", Type: TypeSchedule, Concurrency: ConcurrencyParallel, Target: Target{GraphID: "graph-1"},
			InitialState: map[string]any{"shared": map[string]any{"request": map[string]any{"input": "configured"}}},
			Schedule: &ScheduleSpec{
				Cron:          "0 * * * *",
				StateBindings: &ScheduleStateBindings{Input: "shared.request.input"},
			},
		},
		{
			ID: "chat", Type: TypeChat, Concurrency: ConcurrencyParallel, Target: Target{GraphID: "graph-1"},
			InitialState: map[string]any{"shared": map[string]any{"chat": map[string]any{"message": "configured"}}},
			Chat: &ChatSpec{
				Channel:       "http",
				StateBindings: &ChatStateBindings{Input: "shared.chat.message"},
			},
		},
	}
	for _, item := range tests {
		if err := item.Validate(); err == nil {
			t.Fatalf("overlapping %s trigger state destinations were accepted", item.Type)
		} else if !errors.Is(err, ErrInvalidTrigger) {
			t.Fatalf("overlap error = %v", err)
		}
	}

	unbound := Trigger{
		ID: "unbound", Type: TypeWebhook, Concurrency: ConcurrencyParallel, Target: Target{GraphID: "graph-1"},
		InitialState: map[string]any{"shared": map[string]any{
			"request": map[string]any{"input": "explicit"},
			"trigger": map[string]any{"id": "explicit"},
		}},
		Webhook: &WebhookSpec{},
	}
	if err := unbound.Validate(); err != nil {
		t.Fatalf("unbound initial state destinations were rejected: %v", err)
	}

	siblings := Trigger{
		ID: "siblings", Type: TypeSchedule, Concurrency: ConcurrencyParallel, Target: Target{GraphID: "graph-1"},
		InitialState: map[string]any{"shared": map[string]any{
			"request": map[string]any{"metadata": map[string]any{"source": "explicit"}},
		}},
		Schedule: &ScheduleSpec{
			Cron:          "0 * * * *",
			StateBindings: &ScheduleStateBindings{Input: "shared.request.input"},
		},
	}
	if err := siblings.Validate(); err != nil {
		t.Fatalf("sibling state destinations were rejected: %v", err)
	}
}

func TestTriggerRejectsInvalidWebhookStateMappings(t *testing.T) {
	tests := [][]WebhookStateMapping{
		{{Parameter: "", StatePath: "shared.input"}},
		{{Parameter: "input", StatePath: "runtime.input"}},
		{
			{Parameter: "first", StatePath: "shared.input"},
			{Parameter: "second", StatePath: "shared.input"},
		},
		{
			{Parameter: "first", StatePath: "shared.input"},
			{Parameter: "second", StatePath: "shared.input.value"},
		},
	}
	for _, mappings := range tests {
		item := Trigger{
			ID:          "webhook",
			Type:        TypeWebhook,
			Concurrency: ConcurrencyParallel,
			Target:      Target{GraphID: "graph-1"},
			Webhook:     &WebhookSpec{StateMappings: mappings},
		}
		if err := item.Validate(); err == nil {
			t.Fatalf("mappings %#v were accepted", mappings)
		} else if !errors.Is(err, ErrInvalidStateMapping) {
			t.Fatalf("mapping error = %v", err)
		}
	}
}

func TestTriggerRejectsInvalidChatStateBindings(t *testing.T) {
	tests := []struct {
		name         string
		historyLimit int
		bindings     *ChatStateBindings
	}{
		{name: "negative history limit", historyLimit: -1},
		{name: "history limit above maximum", historyLimit: MaxRecordLimit + 1},
		{name: "invalid section", bindings: &ChatStateBindings{RawHistory: "runtime.chat.history"}},
		{name: "section without field", bindings: &ChatStateBindings{RawHistory: "shared"}},
		{name: "input child", bindings: &ChatStateBindings{Input: "shared.request.input", UserID: "shared.request.input.user_id"}},
		{name: "duplicate bindings", bindings: &ChatStateBindings{TriggerID: "scopes.chat.id", MessageID: "scopes.chat.id"}},
		{name: "overlapping bindings", bindings: &ChatStateBindings{RawHistory: "scopes.chat", UserID: "scopes.chat.user_id"}},
		{name: "conversation messages overlap", bindings: &ChatStateBindings{Conversation: "scopes.chat", UserID: "scopes.chat.messages.user_id"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			item := Trigger{
				ID:          "chat",
				Type:        TypeChat,
				Concurrency: ConcurrencyParallel,
				Target:      Target{GraphID: "graph-1"},
				Chat: &ChatSpec{
					Channel:       "http",
					HistoryLimit:  test.historyLimit,
					StateBindings: test.bindings,
				},
			}
			if err := item.Validate(); err == nil {
				t.Fatal("invalid chat state configuration was accepted")
			} else if !errors.Is(err, ErrInvalidTrigger) {
				t.Fatalf("chat state configuration error = %v", err)
			}
		})
	}
}

func TestTriggerNormalizesChatStateBindings(t *testing.T) {
	now := time.Now().UTC()
	item := Trigger{
		Chat: &ChatSpec{StateBindings: &ChatStateBindings{
			Input:        " shared . request . input ",
			Conversation: " scopes . agent . conversation ",
			RawHistory:   " shared . chat . history ",
			TriggerID:    "  ",
		}},
	}.Normalize(now)
	if item.Chat.StateBindings == nil || item.Chat.StateBindings.Input != "shared.request.input" || item.Chat.StateBindings.Conversation != "scopes.agent.conversation" || item.Chat.StateBindings.RawHistory != "shared.chat.history" {
		t.Fatalf("normalized chat state bindings = %#v", item.Chat.StateBindings)
	}

	empty := Trigger{Chat: &ChatSpec{StateBindings: &ChatStateBindings{RawHistory: "  "}}}.Normalize(now)
	if empty.Chat.StateBindings != nil {
		t.Fatalf("empty chat state bindings = %#v", empty.Chat.StateBindings)
	}
}

func TestTriggerStateBuildersUseOnlyConfiguredBindings(t *testing.T) {
	webhookState, err := buildTriggerState(Trigger{
		ID: "hook", Type: TypeWebhook,
		Webhook: &WebhookSpec{StateBindings: &WebhookStateBindings{Input: "scopes.hook.input"}},
	}, map[string]any{"message": "hello"}, map[string]any{"trace": "trace-1"}, "webhook", "")
	if err != nil {
		t.Fatal(err)
	}
	if input, ok := state.ReadPath(webhookState, "scopes.hook.input"); !ok || input.(map[string]any)["message"] != "hello" {
		t.Fatalf("configured webhook input = %#v", input)
	}
	for _, path := range []string{"shared.request.input", "shared.request.metadata", "shared.trigger"} {
		if value, ok := state.ReadPath(webhookState, path); ok {
			t.Fatalf("unconfigured webhook state %q = %#v", path, value)
		}
	}

	chatState, err := buildChatTriggerState(Trigger{
		ID: "chat", Type: TypeChat,
		Chat: &ChatSpec{StateBindings: &ChatStateBindings{TriggerID: "scopes.chat.trigger_id"}},
	}, chatcap.InboundMessage{Content: "wait for user input"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := state.ReadPath(chatState, "shared.request.input"); ok {
		t.Fatal("chat input was injected without an input binding")
	}
	if triggerID, ok := state.ReadPath(chatState, "scopes.chat.trigger_id"); !ok || triggerID != "chat" {
		t.Fatalf("configured chat trigger id = %#v", triggerID)
	}

	configuredChatState, err := buildChatTriggerState(Trigger{
		ID: "chat", Type: TypeChat,
		Chat: &ChatSpec{StateBindings: &ChatStateBindings{Input: "scopes.chat.input"}},
	}, chatcap.InboundMessage{Content: "hello"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if input, ok := state.ReadPath(configuredChatState, "scopes.chat.input"); !ok || input != "hello" {
		t.Fatalf("configured chat input = %#v", input)
	}
}

func TestServiceRejectsInvalidWebhookPayload(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) {
		return &recordingStarter{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Create(context.Background(), Trigger{
		ID:      "webhook-1",
		Type:    TypeWebhook,
		Enabled: true,
		Target:  Target{GraphID: "graph-1"},
		Webhook: &WebhookSpec{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.InvokeWebhook(context.Background(), "webhook-1", []byte(`not-json`), nil); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("invalid payload error = %v", err)
	}
}

func TestServiceInvokeScheduleAndValidatesCron(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	starter := &recordingStarter{}
	service, err := NewService(store, RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) {
		return starter, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), Trigger{
		ID:       "bad-schedule",
		Type:     TypeSchedule,
		Enabled:  true,
		Target:   Target{GraphID: "graph-1"},
		Schedule: &ScheduleSpec{Cron: "not-a-cron"},
	}); err == nil {
		t.Fatal("invalid cron was accepted")
	}
	_, err = service.Create(context.Background(), Trigger{
		ID:      "schedule-1",
		Type:    TypeSchedule,
		Enabled: true,
		Target:  Target{GraphID: "graph-1"},
		InitialState: map[string]any{
			"shared": map[string]any{"schedule": map[string]any{"attempt": float64(2)}},
		},
		Schedule: &ScheduleSpec{
			Cron:  "0 12 * * *",
			Input: map[string]any{"source": "timer"},
			StateBindings: &ScheduleStateBindings{
				Input:       "scopes.schedule.input",
				Metadata:    "scopes.schedule.metadata",
				TriggerID:   "scopes.schedule.trigger_id",
				TriggerType: "scopes.schedule.trigger_type",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.InvokeSchedule(context.Background(), "schedule-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.RunID != "run-1" {
		t.Fatalf("run id = %q", run.RunID)
	}
	input, ok := state.ReadPath(starter.initial, "scopes.schedule.input")
	if !ok || input.(map[string]any)["source"] != "timer" {
		t.Fatalf("schedule input = %#v", input)
	}
	attempt, ok := state.ReadPath(starter.initial, "shared.schedule.attempt")
	if !ok || attempt != float64(2) {
		t.Fatalf("schedule initial state = %#v", attempt)
	}
	if _, err := service.Get(context.Background(), "schedule-1"); err != nil {
		t.Fatal(err)
	}
}

func TestServiceAsyncRunKeepsSkipConcurrencyActiveUntilCompletion(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	starter := &asyncRecordingStarter{done: done}
	service, err := NewService(store, RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) {
		return starter, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Create(context.Background(), Trigger{
		ID:          "webhook-async",
		Type:        TypeWebhook,
		Enabled:     true,
		Target:      Target{GraphID: "graph-1"},
		Concurrency: ConcurrencySkip,
		Webhook:     &WebhookSpec{},
	})
	if err != nil {
		t.Fatal(err)
	}

	run, err := service.InvokeWebhook(context.Background(), "webhook-async", []byte(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != runtime.RunStatusRunning {
		t.Fatalf("run status = %q, want running", run.Status)
	}
	if _, err := service.InvokeWebhook(context.Background(), "webhook-async", []byte(`{}`), nil); !errors.Is(err, ErrBusy) {
		t.Fatalf("second invocation error = %v, want ErrBusy", err)
	}

	close(done)
	deadline := time.Now().Add(time.Second)
	for {
		service.mu.Lock()
		active := service.activeRuns["webhook-async"]
		service.mu.Unlock()
		if active == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("async trigger remained active after completion")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := service.InvokeWebhook(context.Background(), "webhook-async", []byte(`{}`), nil); err != nil {
		t.Fatalf("invocation after completion error = %v", err)
	}
}

func TestTriggerRequiresGraphIDTarget(t *testing.T) {
	item := Trigger{
		ID:          "webhook",
		Type:        TypeWebhook,
		Concurrency: ConcurrencyParallel,
		Webhook:     &WebhookSpec{},
	}
	if err := item.Validate(); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("Validate() error = %v, want ErrInvalidTarget", err)
	}
}

type failingStore struct {
	Store
	listErr   error
	deleteErr error
}

func (s *failingStore) List(ctx context.Context) ([]Trigger, error) {
	if s.listErr != nil {
		err := s.listErr
		s.listErr = nil
		return nil, err
	}
	return s.Store.List(ctx)
}

func (s *failingStore) Delete(ctx context.Context, id string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	return s.Store.Delete(ctx, id)
}

func TestServiceStartCanRetryAfterStoreFailure(t *testing.T) {
	fileStore, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := &failingStore{Store: fileStore, listErr: errors.New("temporary list failure")}
	service, err := NewService(store, RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) {
		return &recordingStarter{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), Trigger{
		ID:       "schedule-1",
		Type:     TypeSchedule,
		Enabled:  true,
		Target:   Target{GraphID: "graph-1"},
		Schedule: &ScheduleSpec{Cron: "0 12 * * *"},
	}); err != nil {
		t.Fatal(err)
	}

	if err := service.Start(context.Background()); err == nil {
		t.Fatal("first Start() unexpectedly succeeded")
	}
	service.mu.Lock()
	startedAfterFailure := service.cancel != nil
	schedulesAfterFailure := len(service.schedules)
	service.mu.Unlock()
	if startedAfterFailure || schedulesAfterFailure != 0 {
		t.Fatalf("failed Start() left started=%v schedules=%d", startedAfterFailure, schedulesAfterFailure)
	}

	if err := service.Start(context.Background()); err != nil {
		t.Fatalf("retry Start() error = %v", err)
	}
	service.mu.Lock()
	startedAfterRetry := service.cancel != nil
	schedulesAfterRetry := len(service.schedules)
	service.mu.Unlock()
	if !startedAfterRetry || schedulesAfterRetry != 1 {
		t.Fatalf("retried Start() left started=%v schedules=%d", startedAfterRetry, schedulesAfterRetry)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestServiceDeleteFailureKeepsPersistedScheduleActive(t *testing.T) {
	fileStore, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := &failingStore{Store: fileStore}
	service, err := NewService(store, RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) {
		return &recordingStarter{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), Trigger{
		ID:       "schedule-1",
		Type:     TypeSchedule,
		Enabled:  true,
		Target:   Target{GraphID: "graph-1"},
		Schedule: &ScheduleSpec{Cron: "0 12 * * *"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.Close() }()

	store.deleteErr = errors.New("temporary delete failure")
	if err := service.Delete(context.Background(), "schedule-1"); err == nil {
		t.Fatal("Delete() unexpectedly succeeded")
	}
	service.mu.Lock()
	scheduleCount := len(service.schedules)
	service.mu.Unlock()
	if scheduleCount != 1 {
		t.Fatalf("failed Delete() left %d schedules, want 1", scheduleCount)
	}
	if _, err := fileStore.Get(context.Background(), "schedule-1"); err != nil {
		t.Fatalf("failed Delete() removed persisted trigger: %v", err)
	}
}

func TestServiceRecordsStartedAndFailedInvocations(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	starter := &recordingStarter{}
	service, err := NewService(store, RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) {
		return starter, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return clock }
	if _, err := service.Create(context.Background(), Trigger{
		ID:      "webhook-recorded",
		Type:    TypeWebhook,
		Enabled: true,
		Target:  Target{GraphID: "graph-1"},
		Webhook: &WebhookSpec{},
	}); err != nil {
		t.Fatal(err)
	}

	run, err := service.InvokeWebhook(context.Background(), "webhook-recorded", []byte(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.RunID != "run-1" {
		t.Fatalf("run id = %q", run.RunID)
	}

	clock = clock.Add(time.Minute)
	service.resolver = RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) {
		return nil, errors.New("graph unavailable")
	})
	if _, err := service.InvokeWebhook(context.Background(), "webhook-recorded", []byte(`{}`), nil); err == nil {
		t.Fatal("failed invocation unexpectedly succeeded")
	}

	records, err := service.ListRecords(context.Background(), "webhook-recorded", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("record count = %d, want 2", len(records))
	}
	if records[0].Status != runtime.RunStatusFailed || records[0].ErrorMessage != "graph unavailable" {
		t.Fatalf("failed record = %#v", records[0])
	}
	if records[1].Status != runtime.RunStatusCompleted || records[1].Run == nil || records[1].Run.RunID != "run-1" {
		t.Fatalf("started record = %#v", records[1])
	}
}
