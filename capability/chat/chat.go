// Package chat defines the channel-neutral chat reply protocol.
//
// Graph nodes, trigger orchestration, and delivery adapters share
// InboundMessage, Reply, and ReplySink through this package. Concrete sinks
// remain owned by the server and channel packages, while the current sink is
// carried through execution context so the runtime itself stays independent
// of chat delivery.
package chat

import (
	"context"
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

func (message InboundMessage) Normalize() InboundMessage {
	message.ID = strings.TrimSpace(message.ID)
	message.UserID = strings.TrimSpace(message.UserID)
	message.ConversationID = strings.TrimSpace(message.ConversationID)
	message.ChannelConversationID = strings.TrimSpace(message.ChannelConversationID)
	message.Content = strings.TrimSpace(message.Content)
	return message
}

func (message InboundMessage) Validate() error {
	if strings.TrimSpace(message.Content) == "" {
		return errors.New("chat message content is required")
	}
	return nil
}

type ReplyKind string

const (
	ReplyUpdate  ReplyKind = "update"
	ReplyMessage ReplyKind = "message"
	ReplyFinish  ReplyKind = "finish"
)

var ErrReplySinkUnavailable = errors.New("chat reply sink is unavailable")

type Reply struct {
	Kind     ReplyKind `json:"kind"`
	Content  string    `json:"content,omitempty"`
	Error    string    `json:"error,omitempty"`
	NodeID   string    `json:"node_id,omitempty"`
	Sequence int64     `json:"sequence,omitempty"`
}

type ReplySink interface {
	Emit(context.Context, Reply) error
}

type ReplySinkFunc func(context.Context, Reply) error

func (f ReplySinkFunc) Emit(ctx context.Context, reply Reply) error {
	return f(ctx, reply)
}

type replySinkKey struct{}

func WithReplySink(ctx context.Context, sink ReplySink) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, replySinkKey{}, sink)
}

func ReplySinkFromContext(ctx context.Context) ReplySink {
	if ctx == nil {
		return nil
	}
	sink, _ := ctx.Value(replySinkKey{}).(ReplySink)
	return sink
}

func HasReplySink(ctx context.Context) bool {
	return ReplySinkFromContext(ctx) != nil
}

func EmitReply(ctx context.Context, reply Reply) error {
	sink := ReplySinkFromContext(ctx)
	if sink == nil {
		return ErrReplySinkUnavailable
	}
	return sink.Emit(ctx, reply)
}
