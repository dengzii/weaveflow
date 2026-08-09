// Package chat provides state-bound chat response capabilities.
package chat

import (
	"context"
	"errors"
)

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
