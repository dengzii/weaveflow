package trigger

import (
	"context"
	"errors"
	"fmt"
	"strings"

	chatcap "github.com/dengzii/weaveflow/capability/chat"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
	"github.com/google/uuid"
)

type ChatResult struct {
	Run            runtime.RunRecord `json:"run"`
	ConversationID string            `json:"conversation_id,omitempty"`
	Command        string            `json:"command,omitempty"`
	FinalReply     string            `json:"final_reply,omitempty"`
}

func (s *Service) InvokeChat(ctx context.Context, id string, message chatcap.InboundMessage, sink chatcap.ReplySink) (ChatResult, error) {
	if s == nil {
		return ChatResult{}, fmt.Errorf("trigger service is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if sink == nil {
		return ChatResult{}, chatcap.ErrReplySinkUnavailable
	}
	message = message.Normalize()
	if err := message.Validate(); err != nil {
		return ChatResult{}, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	item, err := s.triggerStore.Get(ctx, id)
	if err != nil {
		return ChatResult{}, err
	}
	if item.Type != TypeChat {
		return ChatResult{}, fmt.Errorf("%w: trigger %q is not a chat trigger", ErrTypeMismatch, id)
	}
	if !item.Enabled {
		return ChatResult{}, ErrDisabled
	}
	if message.UserID == "" {
		return ChatResult{}, fmt.Errorf("%w: chat message user_id is required", ErrInvalidPayload)
	}
	if message.ConversationID == "" {
		return ChatResult{}, fmt.Errorf("%w: chat message conversation_id is required", ErrInvalidPayload)
	}
	if result, handled, err := s.handleChatCommand(ctx, item, message, sink); handled {
		return result, err
	}
	executionCtx, message, cleanup, err := s.prepareChatExecution(ctx, item, message)
	if err != nil {
		return ChatResult{}, err
	}
	defer cleanup()
	ctx = executionCtx
	unlock := s.lockChatHistory(item.ID, message.UserID, message.ConversationID)
	defer unlock()
	if err := ctx.Err(); err != nil {
		return ChatResult{ConversationID: message.ConversationID}, err
	}
	var history []ChatHistory
	if limit := chatHistoryLoadLimit(item.Chat); limit > 0 {
		history, err = s.ListChatHistory(ctx, ChatHistoryFilter{
			TriggerID:             item.ID,
			UserID:                message.UserID,
			ChannelConversationID: message.ChannelConversationID,
			ConversationID:        message.ConversationID,
			Limit:                 limit,
		})
		if err != nil {
			return ChatResult{}, fmt.Errorf("load chat history: %w", err)
		}
	}

	now := s.now().UTC()
	chatHistory, err := s.chatHistoryStore.CreateChatHistory(ctx, newPendingChatHistory(item, message, now))
	if err != nil {
		return ChatResult{}, fmt.Errorf("create chat history: %w", err)
	}
	historyRecorder := newChatHistoryRecorder(ctx, s.chatHistoryStore, chatHistory, s.now)
	record := Record{
		ID:          uuid.NewString(),
		TriggerID:   item.ID,
		TriggerType: item.Type,
		Target:      item.Target,
		Status:      runtime.RunStatusPending,
		TriggeredAt: now,
		UpdatedAt:   now,
	}
	if err := s.invocationStore.CreateRecord(ctx, record); err != nil {
		createErr := fmt.Errorf("create trigger record: %w", err)
		return ChatResult{}, errors.Join(createErr, historyRecorder.Finish(ChatResult{}, createErr))
	}

	result, runErr := s.invokeChatRun(ctx, item, message, history, sink, historyRecorder)
	result.ConversationID = message.ConversationID
	historyErr := historyRecorder.Finish(result, runErr)
	record.UpdatedAt = s.now().UTC()
	if result.Run.RunID != "" {
		runCopy := result.Run
		record.Run = &runCopy
		record.Status = result.Run.Status
	}
	if runErr != nil {
		if record.Status == "" || record.Status == runtime.RunStatusPending || record.Status == runtime.RunStatusRunning || record.Status == runtime.RunStatusCompleted {
			record.Status = runtime.RunStatusFailed
		}
		record.ErrorMessage = runErr.Error()
	} else if record.Status == "" || record.Status == runtime.RunStatusPending || record.Status == runtime.RunStatusRunning {
		record.Status = runtime.RunStatusCompleted
	}
	var recordErr error
	if err := s.invocationStore.UpdateRecord(context.WithoutCancel(ctx), record); err != nil {
		recordErr = fmt.Errorf("update trigger record: %w", err)
	}
	return result, errors.Join(runErr, historyErr, recordErr)
}

func (s *Service) invokeChatRun(ctx context.Context, item Trigger, message chatcap.InboundMessage, history []ChatHistory, sink chatcap.ReplySink, historyRecorder *chatHistoryRecorder) (ChatResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	trackActive := false
	if item.Concurrency == ConcurrencySkip {
		s.mu.Lock()
		if s.activeRuns[item.ID] > 0 {
			s.mu.Unlock()
			return ChatResult{}, ErrBusy
		}
		s.activeRuns[item.ID]++
		s.mu.Unlock()
		trackActive = true
	}
	if trackActive {
		defer s.finishActive(item.ID)
	}

	runner, err := s.resolver.Resolve(ctx, item.Target)
	if err != nil {
		return ChatResult{}, err
	}
	if runner == nil {
		return ChatResult{}, fmt.Errorf("runner resolver returned nil")
	}
	initial, err := buildChatTriggerState(item, message, history)
	if err != nil {
		return ChatResult{}, err
	}
	if err := validateRunnerInitialState(runner, initial); err != nil {
		return ChatResult{}, err
	}
	configuredSink := newChatInvocationSink(item.Chat, sink, historyRecorder.RecordReply)
	executionCtx := runtime.WithRunnerEventObserver(ctx, newChatLLMStreamObserver(configuredSink))
	executionCtx = chatcap.WithReplySink(executionCtx, configuredSink)
	executionCtx = runtime.WithRunOrigin(executionCtx, runtime.RunOrigin{
		Type:      string(TypeChat),
		TriggerID: item.ID,
	})
	run, _, runErr := runner.Start(executionCtx, initial)
	finalReply, replySent := configuredSink.finalReply()
	if runErr == nil && !replySent && chatRunFinishContent(run, nil) == "" {
		runErr = ErrChatReplyMissing
	}
	finishContent, finishErr := configuredSink.finish(context.WithoutCancel(ctx), run, runErr)
	if finishContent != "" {
		finalReply = finishContent
	}
	if runErr == nil && finishErr != nil {
		runErr = finishErr
	}
	return ChatResult{Run: run, FinalReply: finalReply}, runErr
}

func chatChannelID(item Trigger) string {
	if item.Chat == nil {
		return ""
	}
	return strings.TrimSpace(item.Chat.Channel)
}

func chatHistoryLoadLimit(spec *ChatSpec) int {
	if spec == nil || spec.HistoryLimit <= 0 || spec.StateBindings == nil {
		return 0
	}
	if spec.StateBindings.Conversation == "" && spec.StateBindings.RawHistory == "" {
		return 0
	}
	return spec.HistoryLimit
}

func buildChatTriggerState(item Trigger, message chatcap.InboundMessage, history []ChatHistory) (*state.State, error) {
	initial := state.FromMap(item.InitialState)
	if item.Chat == nil || item.Chat.StateBindings == nil {
		return initial, nil
	}
	bindings := item.Chat.StateBindings
	if bindings.Input != "" {
		if err := state.SetPath(initial, bindings.Input, message.Content); err != nil {
			return nil, fmt.Errorf("initialize chat input state: %w", err)
		}
	}
	if bindings.Conversation != "" {
		messages, err := chatConversationMessages(history)
		if err != nil {
			return nil, fmt.Errorf("encode chat conversation history: %w", err)
		}
		if err := state.SetPath(initial, chatConversationMessagesPath(bindings.Conversation), messages); err != nil {
			return nil, fmt.Errorf("initialize chat conversation state: %w", err)
		}
	}
	values := []struct {
		name  string
		path  string
		value any
	}{
		{name: "trigger_id", path: bindings.TriggerID, value: item.ID},
		{name: "channel", path: bindings.Channel, value: chatChannelID(item)},
		{name: "user_id", path: bindings.UserID, value: message.UserID},
		{name: "conversation_id", path: bindings.ConversationID, value: message.ConversationID},
		{name: "message_id", path: bindings.MessageID, value: message.ID},
	}
	if bindings.RawHistory != "" {
		values = append(values, struct {
			name  string
			path  string
			value any
		}{name: "raw_history", path: bindings.RawHistory, value: chatHistoryState(history)})
	}
	for _, binding := range values {
		if binding.path == "" {
			continue
		}
		if err := state.SetPath(initial, binding.path, binding.value); err != nil {
			return nil, fmt.Errorf("initialize chat %s state at %q: %w", binding.name, binding.path, err)
		}
	}
	return initial, nil
}

func normalizeChatMessageMetadata(message chatcap.InboundMessage, channel string) chatcap.InboundMessage {
	metadata := make(map[string]any, len(message.Metadata)+4)
	for key, value := range message.Metadata {
		metadata[key] = value
	}
	if _, exists := metadata["channel"]; !exists && channel != "" {
		metadata["channel"] = channel
	}
	if message.UserID != "" {
		metadata["user_id"] = message.UserID
	}
	if message.ConversationID != "" {
		metadata["conversation_id"] = message.ConversationID
	}
	if message.ChannelConversationID != "" {
		metadata["channel_conversation_id"] = message.ChannelConversationID
	}
	if message.ID != "" {
		metadata["message_id"] = message.ID
	}
	message.Metadata = metadata
	return message
}

func (s *Service) lockChatHistory(triggerID, userID, conversationID string) func() {
	if s == nil || strings.TrimSpace(userID) == "" || strings.TrimSpace(conversationID) == "" {
		return func() {}
	}
	key := strings.TrimSpace(triggerID) + "\x00" + strings.TrimSpace(userID) + "\x00" + strings.TrimSpace(conversationID)
	s.mu.Lock()
	if s.chatLocks == nil {
		s.chatLocks = make(map[string]*chatHistoryLock)
	}
	lock := s.chatLocks[key]
	if lock == nil {
		lock = &chatHistoryLock{}
		s.chatLocks[key] = lock
	}
	lock.refs++
	s.mu.Unlock()
	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		s.mu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(s.chatLocks, key)
		}
		s.mu.Unlock()
	}
}
