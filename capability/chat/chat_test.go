package chat

import (
	"context"
	"errors"
	"testing"
)

func TestReplySinkContext(t *testing.T) {
	var received Reply
	ctx := WithReplySink(context.Background(), ReplySinkFunc(func(_ context.Context, reply Reply) error {
		received = reply
		return nil
	}))
	if !HasReplySink(ctx) {
		t.Fatal("reply sink is missing")
	}
	if err := EmitReply(ctx, Reply{Kind: ReplyMessage, Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if received.Kind != ReplyMessage || received.Content != "hello" {
		t.Fatalf("reply = %#v", received)
	}
	if err := EmitReply(context.Background(), Reply{}); !errors.Is(err, ErrReplySinkUnavailable) {
		t.Fatalf("missing sink error = %v", err)
	}
}

func TestMessageValidate(t *testing.T) {
	if err := (Message{}).Validate(); err == nil {
		t.Fatal("empty chat message should fail validation")
	}
	message := (Message{ID: " id ", ConversationID: " chat ", Content: " hello "}).Normalize()
	if message.ID != "id" || message.ConversationID != "chat" || message.Content != "hello" {
		t.Fatalf("normalized message = %#v", message)
	}
}
