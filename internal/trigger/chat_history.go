package trigger

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dengzii/weaveflow/runtime"
)

const ChatHistoryVersion = 2

type ChatMessageDirection string

const (
	ChatMessageInbound  ChatMessageDirection = "inbound"
	ChatMessageOutbound ChatMessageDirection = "outbound"
)

type ChatMessageRole string

const (
	ChatMessageRoleUser      ChatMessageRole = "user"
	ChatMessageRoleAssistant ChatMessageRole = "assistant"
)

type ChatMessageKind string

const (
	ChatMessageInput ChatMessageKind = "input"
	ChatMessageReply ChatMessageKind = "message"
	ChatMessageFinal ChatMessageKind = "final"
)

type ChatTriggerMeta struct {
	Channel    string         `json:"channel"`
	MessageID  string         `json:"message_id,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

type ChatHistoryMessage struct {
	Sequence  int64                `json:"sequence"`
	Direction ChatMessageDirection `json:"direction"`
	Role      ChatMessageRole      `json:"role"`
	Kind      ChatMessageKind      `json:"kind"`
	MessageID string               `json:"message_id,omitempty"`
	NodeID    string               `json:"node_id,omitempty"`
	Content   string               `json:"content"`
	CreatedAt time.Time            `json:"created_at"`
}

type ChatHistory struct {
	Version               int                  `json:"version"`
	ID                    int64                `json:"id"`
	TriggerID             string               `json:"trigger_id"`
	UserID                string               `json:"user_id"`
	ChannelConversationID string               `json:"channel_conversation_id"`
	ConversationID        string               `json:"conversation_id"`
	TriggeredAt           time.Time            `json:"triggered_at"`
	CompletedAt           *time.Time           `json:"completed_at,omitempty"`
	Status                runtime.RunStatus    `json:"status"`
	TriggerMeta           ChatTriggerMeta      `json:"trigger_meta"`
	GraphID               string               `json:"graph_id"`
	RunID                 string               `json:"run_id,omitempty"`
	Messages              []ChatHistoryMessage `json:"messages"`
	FinalAnswer           string               `json:"final_answer,omitempty"`
	ErrorMessage          string               `json:"error_message,omitempty"`
}

type ChatHistoryFilter struct {
	TriggerID             string
	UserID                string
	ChannelConversationID string
	ConversationID        string
	Limit                 int
}

func (s *FileStore) CreateChatHistory(ctx context.Context, history ChatHistory) (ChatHistory, error) {
	if s == nil {
		return ChatHistory{}, fmt.Errorf("trigger store is nil")
	}
	if err := storeContextError(ctx); err != nil {
		return ChatHistory{}, err
	}
	history = normalizeChatHistory(history)
	if history.ID == 0 {
		history.ID = history.TriggeredAt.UnixMilli()
	}
	if err := validateChatHistory(history); err != nil {
		return ChatHistory{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := storeContextError(ctx); err != nil {
		return ChatHistory{}, err
	}
	dir, err := s.ensureChatHistoryDir(history.TriggerID, history.UserID, history.ChannelConversationID, history.ConversationID)
	if err != nil {
		return ChatHistory{}, err
	}
	for {
		path := safeChatHistoryPath(dir, strconv.FormatInt(history.ID, 10)+".json")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := s.writeChatHistoryLocked(ctx, path, history); err != nil {
				return ChatHistory{}, err
			}
			return history, nil
		} else if err != nil {
			return ChatHistory{}, err
		}
		history.ID++
	}
}

func (s *FileStore) UpdateChatHistory(ctx context.Context, history ChatHistory) error {
	if s == nil {
		return fmt.Errorf("trigger store is nil")
	}
	if err := storeContextError(ctx); err != nil {
		return err
	}
	history = normalizeChatHistory(history)
	if err := validateChatHistory(history); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := storeContextError(ctx); err != nil {
		return err
	}
	path := s.chatHistoryPath(history)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	return s.writeChatHistoryLocked(ctx, path, history)
}

func (s *FileStore) ListChatHistory(ctx context.Context, filter ChatHistoryFilter) ([]ChatHistory, error) {
	if s == nil {
		return nil, fmt.Errorf("trigger store is nil")
	}
	if err := storeContextError(ctx); err != nil {
		return nil, err
	}
	filter.TriggerID = strings.TrimSpace(filter.TriggerID)
	filter.UserID = strings.TrimSpace(filter.UserID)
	filter.ChannelConversationID = strings.TrimSpace(filter.ChannelConversationID)
	filter.ConversationID = strings.TrimSpace(filter.ConversationID)
	if err := validateChatHistoryIdentity(filter.TriggerID, filter.UserID, filter.ChannelConversationID, filter.ConversationID); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	dir := s.chatHistoryDir(filter.TriggerID, filter.UserID, filter.ChannelConversationID, filter.ConversationID)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []ChatHistory{}, nil
	}
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		id, err := strconv.ParseInt(strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())), 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("invalid chat history file %q", entry.Name())
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] > ids[j] })
	if filter.Limit > 0 && len(ids) > filter.Limit {
		ids = ids[:filter.Limit]
	}

	items := make([]ChatHistory, 0, len(ids))
	for _, id := range ids {
		if err := storeContextError(ctx); err != nil {
			return nil, err
		}
		path := safeChatHistoryPath(dir, strconv.FormatInt(id, 10)+".json")
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var history ChatHistory
		if err := decodeStoredJSON(data, &history); err != nil {
			return nil, fmt.Errorf("decode chat history %q: %w", path, err)
		}
		if err := validateChatHistory(history); err != nil {
			return nil, fmt.Errorf("decode chat history %q: %w", path, err)
		}
		if history.ID != id || history.TriggerID != filter.TriggerID || history.UserID != filter.UserID || history.ChannelConversationID != filter.ChannelConversationID || history.ConversationID != filter.ConversationID {
			return nil, fmt.Errorf("decode chat history %q: stored identity does not match its path", path)
		}
		items = append(items, history)
	}
	return items, nil
}

func (s *FileStore) ensureChatHistoryDir(triggerID, userID, channelConversationID, conversationID string) (string, error) {
	dir := s.chatHistoryDir(triggerID, userID, channelConversationID, conversationID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func (s *FileStore) chatHistoryDir(triggerID, userID, channelConversationID, conversationID string) string {
	return safeChatHistoryPath(s.chatConversationDir(triggerID, userID, channelConversationID, conversationID), "turns")
}

func (s *FileStore) chatHistoryPath(history ChatHistory) string {
	return safeChatHistoryPath(s.chatHistoryDir(history.TriggerID, history.UserID, history.ChannelConversationID, history.ConversationID), strconv.FormatInt(history.ID, 10)+".json")
}

func (s *FileStore) writeChatHistoryLocked(ctx context.Context, path string, history ChatHistory) error {
	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(filepath.Dir(path), ".chat-history-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := storeContextError(ctx); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func normalizeChatHistory(history ChatHistory) ChatHistory {
	if history.Version == 0 {
		history.Version = ChatHistoryVersion
	}
	history.TriggerID = strings.TrimSpace(history.TriggerID)
	history.UserID = strings.TrimSpace(history.UserID)
	history.ChannelConversationID = strings.TrimSpace(history.ChannelConversationID)
	history.ConversationID = strings.TrimSpace(history.ConversationID)
	history.TriggerMeta.Channel = strings.TrimSpace(history.TriggerMeta.Channel)
	history.TriggerMeta.MessageID = strings.TrimSpace(history.TriggerMeta.MessageID)
	history.GraphID = strings.TrimSpace(history.GraphID)
	history.RunID = strings.TrimSpace(history.RunID)
	return history
}

func validateChatHistory(history ChatHistory) error {
	if history.Version != ChatHistoryVersion {
		return fmt.Errorf("invalid chat history: version %d is unsupported", history.Version)
	}
	if history.ID <= 0 {
		return fmt.Errorf("invalid chat history: id must be a positive Unix millisecond timestamp")
	}
	if err := validateChatHistoryIdentity(history.TriggerID, history.UserID, history.ChannelConversationID, history.ConversationID); err != nil {
		return err
	}
	if history.TriggeredAt.IsZero() {
		return fmt.Errorf("invalid chat history: triggered_at is required")
	}
	if history.Status == "" {
		return fmt.Errorf("invalid chat history: status is required")
	}
	if history.Status != runtime.RunStatusPending && history.Status != runtime.RunStatusRunning && history.CompletedAt == nil {
		return fmt.Errorf("invalid chat history: completed_at is required for terminal status %q", history.Status)
	}
	if history.GraphID == "" {
		return fmt.Errorf("invalid chat history: graph_id is required")
	}
	if len(history.Messages) == 0 {
		return fmt.Errorf("invalid chat history: at least one message is required")
	}
	var previousSequence int64
	var finalMessageCount int
	var finalMessageContent string
	for index, message := range history.Messages {
		if message.Sequence <= previousSequence {
			return fmt.Errorf("invalid chat history: message %d sequence must be increasing", index)
		}
		previousSequence = message.Sequence
		if message.CreatedAt.IsZero() {
			return fmt.Errorf("invalid chat history: message %d created_at is required", index)
		}
		if strings.TrimSpace(message.Content) == "" {
			return fmt.Errorf("invalid chat history: message %d content is required", index)
		}
		if !validChatHistoryMessage(message) {
			return fmt.Errorf("invalid chat history: message %d direction, role, and kind are inconsistent", index)
		}
		if message.Kind == ChatMessageFinal {
			finalMessageCount++
			finalMessageContent = strings.TrimSpace(message.Content)
		}
	}
	if history.FinalAnswer != "" && (finalMessageCount != 1 || finalMessageContent != strings.TrimSpace(history.FinalAnswer)) {
		return fmt.Errorf("invalid chat history: final message must match final_answer")
	}
	if history.FinalAnswer == "" && finalMessageCount > 0 {
		return fmt.Errorf("invalid chat history: final_answer is required when a final message exists")
	}
	if _, err := json.Marshal(history.TriggerMeta.Attributes); err != nil {
		return fmt.Errorf("invalid chat history: trigger_meta attributes must be JSON-compatible: %w", err)
	}
	return nil
}

func validateChatHistoryIdentity(triggerID, userID, channelConversationID, conversationID string) error {
	if err := validateTriggerID(triggerID); err != nil {
		return fmt.Errorf("invalid chat history: trigger_id: %w", err)
	}
	if strings.TrimSpace(userID) == "" {
		return fmt.Errorf("invalid chat history: user_id is required")
	}
	if strings.TrimSpace(channelConversationID) == "" {
		return fmt.Errorf("invalid chat history: channel_conversation_id is required")
	}
	if strings.TrimSpace(conversationID) == "" {
		return fmt.Errorf("invalid chat history: conversation_id is required")
	}
	return nil
}

func validChatHistoryMessage(message ChatHistoryMessage) bool {
	switch message.Direction {
	case ChatMessageInbound:
		return message.Role == ChatMessageRoleUser && message.Kind == ChatMessageInput
	case ChatMessageOutbound:
		return message.Role == ChatMessageRoleAssistant && (message.Kind == ChatMessageReply || message.Kind == ChatMessageFinal)
	default:
		return false
	}
}

func chatHistoryPathSegment(value string) string {
	value = strings.TrimSpace(value)
	if value != "." && value != ".." && isPlainChatHistoryPathSegment(value) {
		return filepath.Base(value)
	}
	return filepath.Base("~" + base64.RawURLEncoding.EncodeToString([]byte(value)))
}

func safeChatHistoryPath(base string, components ...string) string {
	if strings.TrimSpace(base) == "" {
		return ""
	}
	safeComponents := make([]string, len(components))
	for index, component := range components {
		baseComponent := filepath.Base(component)
		if component == "" || component == ".." || strings.Contains(component, "../") || strings.Contains(component, `..\`) || strings.Contains(component, "/") || strings.Contains(component, "\\") || baseComponent != component {
			return ""
		}
		safeComponents[index] = baseComponent
	}
	return filepath.Join(append([]string{base}, safeComponents...)...)
}

func isPlainChatHistoryPathSegment(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}
