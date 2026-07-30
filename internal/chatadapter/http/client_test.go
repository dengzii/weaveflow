package chathttp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	chatcap "github.com/dengzii/weaveflow/capability/chat"
)

func TestClientConsumesChatReplyEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.Header.Get("Accept") != "text/event-stream" {
			t.Fatalf("request = %s accept=%q", request.Method, request.Header.Get("Accept"))
		}
		response.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(response, ": ready\n\nevent: update\ndata: {\"kind\":\"update\",\"content\":\"hel\",\"sequence\":1}\n\nevent: message\ndata: {\"kind\":\"message\",\"content\":\"side\",\"sequence\":2}\n\nevent: finish\ndata: {\"kind\":\"finish\",\"content\":\"hello\",\"sequence\":3}\n\nevent: result\ndata: {}\n\n")
	}))
	defer server.Close()

	var replies []chatcap.Reply
	err := (Client{}).Invoke(context.Background(), server.URL, chatcap.Message{Content: "question"}, chatcap.ReplySinkFunc(func(_ context.Context, reply chatcap.Reply) error {
		replies = append(replies, reply)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(replies) != 3 || replies[0].Kind != chatcap.ReplyUpdate || replies[1].Kind != chatcap.ReplyMessage || replies[2].Kind != chatcap.ReplyFinish {
		t.Fatalf("replies = %#v", replies)
	}
}

func TestClientReturnsStreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(response, "event: error\ndata: {\"error\":\"graph failed\"}\n\n")
	}))
	defer server.Close()

	err := (Client{}).Invoke(context.Background(), server.URL, chatcap.Message{Content: "question"}, chatcap.ReplySinkFunc(func(context.Context, chatcap.Reply) error { return nil }))
	if err == nil || err.Error() != "graph failed" {
		t.Fatalf("error = %v", err)
	}
}
