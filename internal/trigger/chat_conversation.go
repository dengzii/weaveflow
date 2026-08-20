package trigger

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const ChatConversationVersion = 1

type ChatConversationIdentity struct {
	TriggerID             string
	UserID                string
	ChannelConversationID string
}

type ChatConversation struct {
	Version               int       `json:"version"`
	ID                    string    `json:"id"`
	TriggerID             string    `json:"trigger_id"`
	UserID                string    `json:"user_id"`
	ChannelConversationID string    `json:"channel_conversation_id"`
	CreatedAt             time.Time `json:"created_at"`
}

func (s *FileStore) CreateChatConversation(ctx context.Context, conversation ChatConversation) (ChatConversation, error) {
	if s == nil {
		return ChatConversation{}, fmt.Errorf("trigger store is nil")
	}
	if err := storeContextError(ctx); err != nil {
		return ChatConversation{}, err
	}
	conversation = normalizeChatConversation(conversation)
	if conversation.ID == "" {
		conversation.ID = strconv.FormatInt(conversation.CreatedAt.UnixMilli(), 10)
	}
	if err := validateChatConversation(conversation); err != nil {
		return ChatConversation{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := storeContextError(ctx); err != nil {
		return ChatConversation{}, err
	}
	root := s.chatConversationRoot(conversation.TriggerID, conversation.UserID, conversation.ChannelConversationID)
	if err := os.MkdirAll(safeChatHistoryPath(root, "conversations"), 0o700); err != nil {
		return ChatConversation{}, err
	}
	baseID, _ := strconv.ParseInt(conversation.ID, 10, 64)
	for {
		conversation.ID = strconv.FormatInt(baseID, 10)
		dir := s.chatConversationDir(conversation.TriggerID, conversation.UserID, conversation.ChannelConversationID, conversation.ID)
		if _, err := os.Stat(dir); err == nil {
			baseID++
			continue
		} else if !os.IsNotExist(err) {
			return ChatConversation{}, err
		}
		turnsDir := safeChatHistoryPath(dir, "turns")
		if err := os.MkdirAll(turnsDir, 0o700); err != nil {
			return ChatConversation{}, err
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return ChatConversation{}, err
		}
		if err := os.Chmod(turnsDir, 0o700); err != nil {
			return ChatConversation{}, err
		}
		if err := s.writeChatConversationLocked(ctx, safeChatHistoryPath(dir, "conversation.json"), conversation); err != nil {
			return ChatConversation{}, err
		}
		if err := s.writeChatConversationLocked(ctx, safeChatHistoryPath(root, "current.json"), conversation); err != nil {
			return ChatConversation{}, err
		}
		return conversation, nil
	}
}

func (s *FileStore) CurrentChatConversation(ctx context.Context, identity ChatConversationIdentity) (ChatConversation, error) {
	if s == nil {
		return ChatConversation{}, fmt.Errorf("trigger store is nil")
	}
	if err := storeContextError(ctx); err != nil {
		return ChatConversation{}, err
	}
	identity = normalizeChatConversationIdentity(identity)
	if err := validateChatConversationIdentity(identity); err != nil {
		return ChatConversation{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	path := safeChatHistoryPath(s.chatConversationRoot(identity.TriggerID, identity.UserID, identity.ChannelConversationID), "current.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ChatConversation{}, ErrNotFound
	}
	if err != nil {
		return ChatConversation{}, err
	}
	var conversation ChatConversation
	if err := decodeStoredJSON(data, &conversation); err != nil {
		return ChatConversation{}, fmt.Errorf("decode current chat conversation %q: %w", path, err)
	}
	if err := validateChatConversation(conversation); err != nil {
		return ChatConversation{}, fmt.Errorf("decode current chat conversation %q: %w", path, err)
	}
	if conversation.TriggerID != identity.TriggerID || conversation.UserID != identity.UserID || conversation.ChannelConversationID != identity.ChannelConversationID {
		return ChatConversation{}, fmt.Errorf("decode current chat conversation %q: stored identity does not match its path", path)
	}
	return conversation, nil
}

func (s *FileStore) chatConversationRoot(triggerID, userID, channelConversationID string) string {
	return safeChatHistoryPath(
		s.dir,
		"history",
		chatHistoryPathSegment(triggerID),
		chatHistoryPathSegment(userID),
		chatHistoryPathSegment(channelConversationID),
	)
}

func (s *FileStore) chatConversationDir(triggerID, userID, channelConversationID, conversationID string) string {
	return safeChatHistoryPath(s.chatConversationRoot(triggerID, userID, channelConversationID), "conversations", chatHistoryPathSegment(conversationID))
}

func (s *FileStore) writeChatConversationLocked(ctx context.Context, path string, conversation ChatConversation) error {
	data, err := json.MarshalIndent(conversation, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(filepath.Dir(path), ".chat-conversation-*.tmp")
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

func normalizeChatConversation(conversation ChatConversation) ChatConversation {
	if conversation.Version == 0 {
		conversation.Version = ChatConversationVersion
	}
	conversation.ID = strings.TrimSpace(conversation.ID)
	conversation.TriggerID = strings.TrimSpace(conversation.TriggerID)
	conversation.UserID = strings.TrimSpace(conversation.UserID)
	conversation.ChannelConversationID = strings.TrimSpace(conversation.ChannelConversationID)
	return conversation
}

func normalizeChatConversationIdentity(identity ChatConversationIdentity) ChatConversationIdentity {
	identity.TriggerID = strings.TrimSpace(identity.TriggerID)
	identity.UserID = strings.TrimSpace(identity.UserID)
	identity.ChannelConversationID = strings.TrimSpace(identity.ChannelConversationID)
	return identity
}

func validateChatConversation(conversation ChatConversation) error {
	if conversation.Version != ChatConversationVersion {
		return fmt.Errorf("invalid chat conversation: version %d is unsupported", conversation.Version)
	}
	id, err := strconv.ParseInt(conversation.ID, 10, 64)
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid chat conversation: id must be a Unix millisecond timestamp")
	}
	if err := validateChatConversationIdentity(ChatConversationIdentity{
		TriggerID:             conversation.TriggerID,
		UserID:                conversation.UserID,
		ChannelConversationID: conversation.ChannelConversationID,
	}); err != nil {
		return err
	}
	if conversation.CreatedAt.IsZero() {
		return fmt.Errorf("invalid chat conversation: created_at is required")
	}
	return nil
}

func validateChatConversationIdentity(identity ChatConversationIdentity) error {
	if err := validateTriggerID(identity.TriggerID); err != nil {
		return fmt.Errorf("invalid chat conversation: trigger_id: %w", err)
	}
	if identity.UserID == "" {
		return fmt.Errorf("invalid chat conversation: user_id is required")
	}
	if identity.ChannelConversationID == "" {
		return fmt.Errorf("invalid chat conversation: channel_conversation_id is required")
	}
	return nil
}
