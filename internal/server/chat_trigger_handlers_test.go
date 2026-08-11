package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	request := httptest.NewRequest(http.MethodPost, "/graphs/graph/triggers/chat/chat", strings.NewReader(`{"message_id":"m1","user_id":"u1","conversation_id":"c1","content":"hello"}`))
	engine.ServeHTTP(buffered, request)
	if buffered.Code != http.StatusOK {
		t.Fatalf("buffered status = %d body = %s", buffered.Code, buffered.Body.String())
	}
	var response struct {
		Data struct {
			Result  trigger.ChatResult `json:"result"`
			Replies []chatcap.Reply    `json:"replies"`
		} `json:"data"`
	}
	if err := json.Unmarshal(buffered.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data.Replies) != 2 || response.Data.Replies[0].Kind != chatcap.ReplyMessage || response.Data.Replies[1].Kind != chatcap.ReplyFinish {
		t.Fatalf("buffered replies = %#v", response.Data.Replies)
	}
	if response.Data.Result.FinalReply != "first" || response.Data.Result.ConversationID == "" || response.Data.Result.ConversationID == "c1" || response.Data.Replies[1].Content != "" {
		t.Fatalf("buffered result = %#v", response.Data.Result)
	}

	streamed := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/graphs/graph/triggers/chat/chat", strings.NewReader(`{"user_id":"u1","conversation_id":"c1","content":"hello"}`))
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

	newConversation := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/graphs/graph/triggers/chat/chat", strings.NewReader(`{"user_id":"u1","conversation_id":"c1","content":"/new"}`))
	engine.ServeHTTP(newConversation, request)
	if newConversation.Code != http.StatusOK {
		t.Fatalf("new conversation status = %d body = %s", newConversation.Code, newConversation.Body.String())
	}
	var newResponse struct {
		Data struct {
			Result  trigger.ChatResult `json:"result"`
			Replies []chatcap.Reply    `json:"replies"`
		} `json:"data"`
	}
	if err := json.Unmarshal(newConversation.Body.Bytes(), &newResponse); err != nil {
		t.Fatal(err)
	}
	if newResponse.Data.Result.Command != "/new" || newResponse.Data.Result.ConversationID == "" || newResponse.Data.Result.ConversationID == response.Data.Result.ConversationID || len(newResponse.Data.Replies) != 1 {
		t.Fatalf("new conversation response = %#v", newResponse.Data)
	}

	missingIdentity := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/graphs/graph/triggers/chat/chat", strings.NewReader(`{"content":"hello"}`))
	engine.ServeHTTP(missingIdentity, request)
	if missingIdentity.Code != http.StatusBadRequest {
		t.Fatalf("missing identity status = %d body = %s", missingIdentity.Code, missingIdentity.Body.String())
	}
}

func TestChatTriggerRunControlUpdatesReplyChannel(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		status   runtime.RunStatus
		reply    string
	}{
		{name: "pause", endpoint: "pause", status: runtime.RunStatusPaused, reply: "Run paused."},
		{name: "cancel", endpoint: "cancel", status: runtime.RunStatusCanceled, reply: "Response stopped."},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			started := make(chan struct{})
			release := make(chan struct{})
			defer close(release)
			graph := newRunControlTestGraph(t, started, release, true)
			srv, err := New(context.Background(), Config{
				Graph:          graph,
				BaseDir:        t.TempDir(),
				GraphID:        "chat-control-graph",
				GraphVersion:   "v1",
				GraphSessionID: "chat-control-session",
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := srv.triggers.Create(context.Background(), trigger.Trigger{
				ID:      "controlled-chat",
				Type:    trigger.TypeChat,
				Enabled: true,
				Target:  trigger.Target{GraphID: "chat-control-graph"},
				Chat:    &trigger.ChatSpec{},
			}); err != nil {
				t.Fatal(err)
			}

			type invocationResult struct {
				result trigger.ChatResult
				err    error
			}
			invocationDone := make(chan invocationResult, 1)
			replies := make(chan chatcap.Reply, 2)
			go func() {
				result, err := srv.triggers.InvokeChat(context.Background(), "controlled-chat", chatcap.InboundMessage{
					ID: "message-1", UserID: "user-1", ConversationID: "conversation-1", Content: "hello",
				}, chatcap.ReplySinkFunc(func(_ context.Context, reply chatcap.Reply) error {
					replies <- reply
					return nil
				}))
				invocationDone <- invocationResult{result: result, err: err}
			}()

			waitForSignal(t, started, "chat trigger run node start")
			runID := waitForServerRunID(t, srv.Runner())
			engine := gin.New()
			srv.RegisterRoutes(engine.Group(""))
			response := serveHTTP(engine, http.MethodPost, "/graphs/chat-control-graph/runs/"+runID+"/"+test.endpoint, "")
			controlledRun := decodeRunRecordResponse(t, response, http.StatusOK)
			if controlledRun.Status != test.status {
				t.Fatalf("control response status = %q, want %q", controlledRun.Status, test.status)
			}

			select {
			case invocation := <-invocationDone:
				if invocation.err != nil {
					t.Fatalf("InvokeChat() error = %v", invocation.err)
				}
				if invocation.result.Run.Status != test.status || invocation.result.FinalReply != test.reply {
					t.Fatalf("chat invocation result = %#v", invocation.result)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("timed out waiting for chat invocation")
			}

			select {
			case reply := <-replies:
				if reply.Kind != chatcap.ReplyFinish || reply.Content != test.reply || reply.Error != "" {
					t.Fatalf("terminal reply = %#v", reply)
				}
			default:
				t.Fatal("chat channel did not receive terminal reply")
			}
			select {
			case reply := <-replies:
				t.Fatalf("unexpected additional reply = %#v", reply)
			default:
			}
		})
	}
}
