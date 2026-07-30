package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	chatcap "github.com/dengzii/weaveflow/capability/chat"
	"github.com/dengzii/weaveflow/internal/trigger"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
	"github.com/gin-gonic/gin"
)

type chatHandlerStarter struct{}

func (chatHandlerStarter) Start(ctx context.Context, initial *state.State) (runtime.RunRecord, *state.State, error) {
	if err := chatcap.EmitReply(ctx, chatcap.Reply{Kind: chatcap.ReplyMessage, Content: "first", NodeID: "reply"}); err != nil {
		return runtime.RunRecord{}, initial, err
	}
	if err := state.SetPath(initial, "shared.final.answer", "final"); err != nil {
		return runtime.RunRecord{}, initial, err
	}
	return runtime.RunRecord{RunID: "chat-run", Status: runtime.RunStatusCompleted}, initial, nil
}

func TestChatTriggerRouteSupportsBufferedAndStreamingReplies(t *testing.T) {
	store, err := trigger.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := trigger.NewService(store, trigger.RunnerResolverFunc(func(context.Context, trigger.Target) (trigger.RunStarter, error) {
		return chatHandlerStarter{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), trigger.Trigger{
		ID: "chat", Type: trigger.TypeChat, Enabled: true, Target: trigger.Target{GraphID: "graph"},
		Chat: &trigger.ChatSpec{},
	}); err != nil {
		t.Fatal(err)
	}
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir(), TriggerService: service})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	buffered := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/triggers/chat/chat", strings.NewReader(`{"message_id":"m1","conversation_id":"c1","content":"hello"}`))
	engine.ServeHTTP(buffered, request)
	if buffered.Code != http.StatusOK {
		t.Fatalf("buffered status = %d body = %s", buffered.Code, buffered.Body.String())
	}
	var response struct {
		Data struct {
			Replies []chatcap.Reply `json:"replies"`
		} `json:"data"`
	}
	if err := json.Unmarshal(buffered.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data.Replies) != 2 || response.Data.Replies[0].Kind != chatcap.ReplyMessage || response.Data.Replies[1].Kind != chatcap.ReplyFinish {
		t.Fatalf("buffered replies = %#v", response.Data.Replies)
	}

	streamed := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/triggers/chat/chat", strings.NewReader(`{"content":"hello"}`))
	request.Header.Set("Accept", "text/event-stream")
	engine.ServeHTTP(streamed, request)
	if streamed.Code != http.StatusOK || streamed.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream status = %d content-type = %q body = %s", streamed.Code, streamed.Header().Get("Content-Type"), streamed.Body.String())
	}
	for _, expected := range []string{"event: message", "event: finish", "event: result"} {
		if !strings.Contains(streamed.Body.String(), expected) {
			t.Fatalf("stream body missing %q: %s", expected, streamed.Body.String())
		}
	}
}
