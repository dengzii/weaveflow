package node

import (
	"context"
	"testing"

	chatcap "github.com/dengzii/weaveflow/capability/chat"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/state"
)

func TestChatReplyNodeEmitsStandaloneMessage(t *testing.T) {
	target := NewChatReplyNode(WithID("reply"))
	target.InputPath = state.Shared("draft")
	initial := state.FromShared(map[string]any{"draft": "hello"})
	var received chatcap.Reply
	ctx := chatcap.WithReplySink(context.Background(), chatcap.ReplySinkFunc(func(_ context.Context, reply chatcap.Reply) error {
		received = reply
		return nil
	}))
	if _, err := core.ExecuteNode(ctx, initial, target); err != nil {
		t.Fatal(err)
	}
	if received.Kind != chatcap.ReplyMessage || received.Content != "hello" || received.NodeID != "reply" {
		t.Fatalf("reply = %#v", received)
	}
}

func TestChatReplyNodeRequiresChatCapability(t *testing.T) {
	target := NewChatReplyNode(WithID("reply"))
	target.Content = "hello"
	if _, err := core.ExecuteNode(context.Background(), state.NewState(), target); err == nil {
		t.Fatal("chat reply without a sink should fail")
	}
}
