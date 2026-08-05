package chatchannel

import (
	"errors"
	"strings"
)

type InboundMessage struct {
	ID                    string         `json:"message_id,omitempty"`
	UserID                string         `json:"user_id,omitempty"`
	ConversationID        string         `json:"conversation_id,omitempty"`
	ChannelConversationID string         `json:"-"`
	Content               string         `json:"content"`
	Metadata              map[string]any `json:"metadata,omitempty"`
}

func (m InboundMessage) Normalize() InboundMessage {
	m.ID = strings.TrimSpace(m.ID)
	m.UserID = strings.TrimSpace(m.UserID)
	m.ConversationID = strings.TrimSpace(m.ConversationID)
	m.ChannelConversationID = strings.TrimSpace(m.ChannelConversationID)
	m.Content = strings.TrimSpace(m.Content)
	return m
}

func (m InboundMessage) Validate() error {
	if strings.TrimSpace(m.Content) == "" {
		return errors.New("chat message content is required")
	}
	return nil
}
