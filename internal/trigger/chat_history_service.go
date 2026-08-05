package trigger

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	chatcap "github.com/dengzii/weaveflow/capability/chat"
	"github.com/dengzii/weaveflow/capability/conversation"
	"github.com/dengzii/weaveflow/internal/chatchannel"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/tmc/langchaingo/llms"
)

var persistedChatMetadataKeys = map[string]struct{}{
	"channel":                 {},
	"channel_conversation_id": {},
	"message_id":              {},
	"message_state":           {},
	"message_type":            {},
	"request_id":              {},
	"sender_id":               {},
	"seq":                     {},
	"session_id":              {},
}

type chatHistoryRecorder struct {
	mu      sync.Mutex
	store   ChatHistoryStore
	history ChatHistory
	now     func() time.Time
	ctx     context.Context
}

func newPendingChatHistory(item Trigger, message chatchannel.InboundMessage, now time.Time) ChatHistory {
	return ChatHistory{
		Version:               ChatHistoryVersion,
		TriggerID:             item.ID,
		UserID:                message.UserID,
		ChannelConversationID: message.ChannelConversationID,
		ConversationID:        message.ConversationID,
		TriggeredAt:           now,
		Status:                runtime.RunStatusPending,
		TriggerMeta: ChatTriggerMeta{
			Channel:    chatChannelID(item),
			MessageID:  message.ID,
			Attributes: persistedChatMetadata(message.Metadata),
		},
		GraphID: item.Target.GraphID,
		Messages: []ChatHistoryMessage{{
			Sequence:  1,
			Direction: ChatMessageInbound,
			Role:      ChatMessageRoleUser,
			Kind:      ChatMessageInput,
			MessageID: message.ID,
			Content:   message.Content,
			CreatedAt: now,
		}},
	}
}

func newChatHistoryRecorder(ctx context.Context, store ChatHistoryStore, history ChatHistory, now func() time.Time) *chatHistoryRecorder {
	if ctx == nil {
		ctx = context.Background()
	}
	if now == nil {
		now = time.Now
	}
	return &chatHistoryRecorder{
		store:   store,
		history: history,
		now:     now,
		ctx:     context.WithoutCancel(ctx),
	}
}

func (r *chatHistoryRecorder) RecordReply(_ context.Context, reply chatcap.Reply) error {
	if r == nil || reply.Kind == chatcap.ReplyUpdate {
		return nil
	}
	content := strings.TrimSpace(reply.Content)
	if content == "" {
		return nil
	}
	kind := ChatMessageReply
	if reply.Kind == chatcap.ReplyFinish {
		kind = ChatMessageFinal
	} else if reply.Kind != chatcap.ReplyMessage {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	sequence := r.history.Messages[len(r.history.Messages)-1].Sequence + 1
	previousFinalAnswer := r.history.FinalAnswer
	r.history.Messages = append(r.history.Messages, ChatHistoryMessage{
		Sequence:  sequence,
		Direction: ChatMessageOutbound,
		Role:      ChatMessageRoleAssistant,
		Kind:      kind,
		NodeID:    strings.TrimSpace(reply.NodeID),
		Content:   content,
		CreatedAt: r.now().UTC(),
	})
	if kind == ChatMessageFinal {
		r.history.FinalAnswer = content
	}
	if err := r.store.UpdateChatHistory(r.ctx, r.history); err != nil {
		r.history.Messages = r.history.Messages[:len(r.history.Messages)-1]
		r.history.FinalAnswer = previousFinalAnswer
		return fmt.Errorf("persist chat reply: %w", err)
	}
	return nil
}

func (r *chatHistoryRecorder) Finish(result ChatResult, runErr error) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	completedAt := r.now().UTC()
	r.history.CompletedAt = &completedAt
	r.history.FinalAnswer = strings.TrimSpace(result.FinalReply)
	if r.history.FinalAnswer == "" {
		for index := len(r.history.Messages) - 1; index >= 0; index-- {
			if r.history.Messages[index].Kind == ChatMessageFinal {
				r.history.FinalAnswer = r.history.Messages[index].Content
				break
			}
		}
	}
	if result.Run.GraphID != "" {
		r.history.GraphID = result.Run.GraphID
	}
	r.history.RunID = result.Run.RunID
	r.history.Status = result.Run.Status
	if runErr != nil {
		r.history.ErrorMessage = runErr.Error()
		if r.history.Status == "" || r.history.Status == runtime.RunStatusPending || r.history.Status == runtime.RunStatusRunning {
			r.history.Status = runtime.RunStatusFailed
		}
	} else if r.history.Status == "" || r.history.Status == runtime.RunStatusPending || r.history.Status == runtime.RunStatusRunning {
		r.history.Status = runtime.RunStatusCompleted
	}
	r.ensureFinalMessage(completedAt)
	if err := r.store.UpdateChatHistory(r.ctx, r.history); err != nil {
		return fmt.Errorf("finalize chat history: %w", err)
	}
	return nil
}

func (r *chatHistoryRecorder) ensureFinalMessage(createdAt time.Time) {
	if r.history.FinalAnswer == "" {
		return
	}
	for _, message := range r.history.Messages {
		if message.Kind == ChatMessageFinal {
			return
		}
	}
	lastIndex := len(r.history.Messages) - 1
	if lastIndex >= 0 && r.history.Messages[lastIndex].Direction == ChatMessageOutbound && r.history.Messages[lastIndex].Content == r.history.FinalAnswer {
		r.history.Messages[lastIndex].Kind = ChatMessageFinal
		return
	}
	sequence := int64(1)
	if lastIndex >= 0 {
		sequence = r.history.Messages[lastIndex].Sequence + 1
	}
	r.history.Messages = append(r.history.Messages, ChatHistoryMessage{
		Sequence:  sequence,
		Direction: ChatMessageOutbound,
		Role:      ChatMessageRoleAssistant,
		Kind:      ChatMessageFinal,
		Content:   r.history.FinalAnswer,
		CreatedAt: createdAt,
	})
}

func persistedChatMetadata(metadata map[string]any) map[string]any {
	attributes := make(map[string]any)
	for key, value := range metadata {
		if _, ok := persistedChatMetadataKeys[strings.ToLower(strings.TrimSpace(key))]; ok {
			attributes[key] = value
		}
	}
	if len(attributes) == 0 {
		return nil
	}
	return attributes
}

func chatHistoryState(records []ChatHistory) []any {
	items := make([]any, 0, len(records))
	for index := len(records) - 1; index >= 0; index-- {
		record := records[index]
		if record.Status != runtime.RunStatusCompleted {
			continue
		}
		messages := make([]any, 0, len(record.Messages))
		for _, message := range record.Messages {
			messages = append(messages, map[string]any{
				"sequence":   message.Sequence,
				"direction":  string(message.Direction),
				"role":       string(message.Role),
				"kind":       string(message.Kind),
				"message_id": message.MessageID,
				"node_id":    message.NodeID,
				"content":    message.Content,
				"created_at": message.CreatedAt.UTC().Format(time.RFC3339Nano),
			})
		}
		items = append(items, map[string]any{
			"id":                      record.ID,
			"trigger_id":              record.TriggerID,
			"user_id":                 record.UserID,
			"channel_conversation_id": record.ChannelConversationID,
			"conversation_id":         record.ConversationID,
			"trigger_meta":            record.TriggerMeta,
			"graph_id":                record.GraphID,
			"run_id":                  record.RunID,
			"messages":                messages,
			"final_answer":            record.FinalAnswer,
			"triggered_at":            record.TriggeredAt.UTC().Format(time.RFC3339Nano),
		})
	}
	return items
}

func chatConversationMessages(records []ChatHistory) ([]any, error) {
	messages := make([]llms.MessageContent, 0)
	for index := len(records) - 1; index >= 0; index-- {
		record := records[index]
		if record.Status != runtime.RunStatusCompleted {
			continue
		}
		for _, message := range record.Messages {
			var role llms.ChatMessageType
			switch message.Role {
			case ChatMessageRoleUser:
				role = llms.ChatMessageTypeHuman
			case ChatMessageRoleAssistant:
				role = llms.ChatMessageTypeAI
			default:
				return nil, fmt.Errorf("unsupported chat history message role %q", message.Role)
			}
			messages = append(messages, llms.TextParts(role, message.Content))
		}
	}
	return conversation.EncodeMessages(messages)
}
