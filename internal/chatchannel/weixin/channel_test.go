package weixin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	chatcap "github.com/dengzii/weaveflow/capability/chat"
	"github.com/dengzii/weaveflow/internal/chatchannel"
)

func TestRegisterWithCursorDirectoryUsesManagedCursorPath(t *testing.T) {
	cursorDirectory := filepath.Join(t.TempDir(), "weixin")
	channels := chatchannel.NewRegistry()
	if err := RegisterWithCursorDirectory(channels, cursorDirectory); err != nil {
		t.Fatal(err)
	}
	instance, err := channels.NewInstance(ChannelID, chatchannel.InstanceConfig{
		TriggerID: "team/chat:primary",
		Handler:   chatchannel.HandlerFunc(func(context.Context, chatchannel.InboundMessage, chatcap.ReplySink) error { return nil }),
		Config:    map[string]any{"bot_token": "token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	channel, ok := instance.(*Channel)
	if !ok {
		t.Fatalf("instance type = %T", instance)
	}
	want := filepath.Join(cursorDirectory, "team_chat_primary.sync")
	if channel.config.CursorFile != want {
		t.Fatalf("cursor file = %q, want %q", channel.config.CursorFile, want)
	}
	if channel.legacyCursorFile != cursorFile(DefaultCursorDirectory, "team/chat:primary") {
		t.Fatalf("legacy cursor file = %q", channel.legacyCursorFile)
	}
}

func TestFactoryExplicitCursorFileOverridesManagedDirectory(t *testing.T) {
	explicitCursorFile := filepath.Join(t.TempDir(), "custom", "cursor.sync")
	instance, err := (Factory{
		CursorDirectory:       filepath.Join(t.TempDir(), "managed"),
		LegacyCursorDirectory: filepath.Join(t.TempDir(), "legacy"),
	}).New(chatchannel.InstanceConfig{
		TriggerID: "explicit",
		Config: map[string]any{
			"bot_token":   "token",
			"cursor_file": explicitCursorFile,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	channel := instance.(*Channel)
	if channel.config.CursorFile != explicitCursorFile {
		t.Fatalf("cursor file = %q, want %q", channel.config.CursorFile, explicitCursorFile)
	}
	if channel.legacyCursorFile != "" {
		t.Fatalf("legacy cursor file = %q, want empty", channel.legacyCursorFile)
	}
}

func TestChannelMigratesLegacyCursorBeforePolling(t *testing.T) {
	rootDirectory := t.TempDir()
	legacyDirectory := filepath.Join(rootDirectory, "legacy")
	cursorDirectory := filepath.Join(rootDirectory, "server", "weixin")
	triggerID := "migrate"
	legacyCursorFile := cursorFile(legacyDirectory, triggerID)
	if err := writeCursor(legacyCursorFile, "legacy-cursor"); err != nil {
		t.Fatal(err)
	}

	receivedCursor := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/ilink/bot/getupdates" {
			http.NotFound(response, request)
			return
		}
		var payload getUpdatesRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode getupdates request: %v", err)
			return
		}
		receivedCursor <- payload.Cursor
		_ = json.NewEncoder(response).Encode(map[string]any{"ret": -14, "errmsg": "stop after first poll"})
	}))
	defer server.Close()

	instance, err := (Factory{
		HTTPClient:            server.Client(),
		CursorDirectory:       cursorDirectory,
		LegacyCursorDirectory: legacyDirectory,
	}).New(chatchannel.InstanceConfig{
		TriggerID: triggerID,
		Handler:   chatchannel.HandlerFunc(func(context.Context, chatchannel.InboundMessage, chatcap.ReplySink) error { return nil }),
		Config:    map[string]any{"bot_token": "token", "base_url": server.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = instance.Run(context.Background())
	var tokenErr *tokenError
	if !errors.As(err, &tokenErr) {
		t.Fatalf("Run() error = %v", err)
	}
	select {
	case cursor := <-receivedCursor:
		if cursor != "legacy-cursor" {
			t.Fatalf("first poll cursor = %q", cursor)
		}
	case <-time.After(time.Second):
		t.Fatal("first poll was not received")
	}
	targetCursorFile := cursorFile(cursorDirectory, triggerID)
	cursor, err := readCursor(targetCursorFile)
	if err != nil || cursor != "legacy-cursor" {
		t.Fatalf("migrated cursor = %q, err = %v", cursor, err)
	}
	if _, err := os.Stat(legacyCursorFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy cursor still exists: %v", err)
	}
}

func TestMigrateCursorFileKeepsExistingTarget(t *testing.T) {
	rootDirectory := t.TempDir()
	legacyCursorFile := filepath.Join(rootDirectory, "legacy", "cursor.sync")
	targetCursorFile := filepath.Join(rootDirectory, "server", "weixin", "cursor.sync")
	if err := writeCursor(legacyCursorFile, "stale-cursor"); err != nil {
		t.Fatal(err)
	}
	if err := writeCursor(targetCursorFile, "current-cursor"); err != nil {
		t.Fatal(err)
	}
	if err := migrateCursorFile(legacyCursorFile, targetCursorFile); err != nil {
		t.Fatal(err)
	}
	cursor, err := readCursor(targetCursorFile)
	if err != nil || cursor != "current-cursor" {
		t.Fatalf("target cursor = %q, err = %v", cursor, err)
	}
	if _, err := os.Stat(legacyCursorFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy cursor still exists: %v", err)
	}
}

func TestChannelPollsAndSendsWithOfficialHeadersAndCursor(t *testing.T) {
	type receivedUpdate struct {
		request getUpdatesRequest
		headers http.Header
	}
	type receivedSend struct {
		request sendMessageRequest
		headers http.Header
	}
	type receivedTyping struct {
		request sendTypingRequest
		headers http.Header
	}

	var mu sync.Mutex
	var updates []receivedUpdate
	var sends []receivedSend
	var typings []receivedTyping
	sendSeen := make(chan struct{}, 2)
	secondPollSeen := make(chan struct{}, 1)
	cursorFile := filepathForTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/ilink/bot/getupdates":
			var payload getUpdatesRequest
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode getupdates request: %v", err)
				return
			}
			mu.Lock()
			updates = append(updates, receivedUpdate{request: payload, headers: request.Header.Clone()})
			call := len(updates)
			mu.Unlock()
			if call == 1 {
				_ = json.NewEncoder(response).Encode(map[string]any{
					"ret": 0,
					"msgs": []any{map[string]any{
						"seq":           7,
						"message_id":    12345,
						"from_user_id":  "user-a",
						"to_user_id":    "bot-a",
						"session_id":    "session-a",
						"message_type":  1,
						"message_state": 0,
						"context_token": "ctx-a",
						"item_list":     []any{map[string]any{"type": 1, "text_item": map[string]string{"text": "hello"}}},
					}},
					"get_updates_buf": "cursor-1",
				})
				return
			}
			secondPollSeen <- struct{}{}
			<-request.Context().Done()
		case "/ilink/bot/sendmessage":
			var payload sendMessageRequest
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode sendmessage request: %v", err)
				return
			}
			mu.Lock()
			sends = append(sends, receivedSend{request: payload, headers: request.Header.Clone()})
			mu.Unlock()
			_ = json.NewEncoder(response).Encode(map[string]any{"ret": 0})
			sendSeen <- struct{}{}
		case "/ilink/bot/getconfig":
			var payload getTypingConfigRequest
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode getconfig request: %v", err)
				return
			}
			if payload.WeChatUserID != "user-a" || payload.ContextToken != "ctx-a" {
				t.Errorf("getconfig request = %#v", payload)
			}
			_ = json.NewEncoder(response).Encode(map[string]any{"ret": 0, "typing_ticket": "ticket-a"})
		case "/ilink/bot/sendtyping":
			var payload sendTypingRequest
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode sendtyping request: %v", err)
				return
			}
			mu.Lock()
			typings = append(typings, receivedTyping{request: payload, headers: request.Header.Clone()})
			mu.Unlock()
			_ = json.NewEncoder(response).Encode(map[string]any{"ret": 0})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	instance, err := (Factory{Logger: discardLogger(), HTTPClient: server.Client()}).New(chatchannel.InstanceConfig{
		TriggerID: "chat-a",
		Handler: chatchannel.HandlerFunc(func(ctx context.Context, message chatchannel.InboundMessage, sink chatcap.ReplySink) error {
			if message.ID != "12345" || message.UserID != "user-a" || message.ConversationID != "user-a" || message.Content != "hello" {
				t.Errorf("message = %#v", message)
			}
			if message.Metadata["channel"] != ChannelID || message.Metadata["session_id"] != "session-a" {
				t.Errorf("metadata = %#v", message.Metadata)
			}
			for _, reply := range []chatcap.Reply{
				{Kind: chatcap.ReplyUpdate, Content: "partial"},
				{Kind: chatcap.ReplyMessage, Content: "side message"},
				{Kind: chatcap.ReplyFinish, Content: "final answer"},
			} {
				if err := sink.Emit(ctx, reply); err != nil {
					return err
				}
			}
			return nil
		}),
		Config: map[string]any{
			"bot_token":   "secret-token",
			"base_url":    server.URL,
			"cursor_file": cursorFile,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- instance.Run(ctx) }()

	for range 2 {
		select {
		case <-sendSeen:
		case <-time.After(5 * time.Second):
			cancel()
			t.Fatal("timed out waiting for iLink replies")
		}
	}
	select {
	case <-secondPollSeen:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("timed out waiting for cursor commit")
	}
	mu.Lock()
	gotUpdates := append([]receivedUpdate(nil), updates...)
	gotSends := append([]receivedSend(nil), sends...)
	gotTypings := append([]receivedTyping(nil), typings...)
	mu.Unlock()
	if len(gotUpdates) == 0 || gotUpdates[0].request.Cursor != "" {
		t.Fatalf("getupdates requests = %#v", gotUpdates)
	}
	if gotUpdates[0].request.BaseInfo.ChannelVersion != "2.4.6" || gotUpdates[0].request.BaseInfo.BotAgent != botAgent {
		t.Fatalf("base_info = %#v", gotUpdates[0].request.BaseInfo)
	}
	for index, item := range gotSends {
		if item.headers.Get("AuthorizationType") != "ilink_bot_token" || item.headers.Get("Authorization") != "Bearer secret-token" {
			t.Fatalf("send %d auth headers = %#v", index, item.headers)
		}
		if item.headers.Get("iLink-App-Id") != apiID || item.headers.Get("iLink-App-ClientVersion") != apiClientVersion {
			t.Fatalf("send %d app headers = %#v", index, item.headers)
		}
		uin, err := base64.StdEncoding.DecodeString(item.headers.Get("X-WECHAT-UIN"))
		if err != nil {
			t.Fatalf("decode X-WECHAT-UIN: %v", err)
		}
		if _, err := strconv.ParseUint(string(uin), 10, 32); err != nil {
			t.Fatalf("X-WECHAT-UIN = %q: %v", uin, err)
		}
		if item.request.Message.ToUserID != "user-a" || item.request.Message.ContextToken != "ctx-a" {
			t.Fatalf("send %d message target = %#v", index, item.request.Message)
		}
		if item.request.Message.MessageType != 2 || item.request.Message.MessageState != 2 {
			t.Fatalf("send %d message type/state = %#v", index, item.request.Message)
		}
		if len(item.request.Message.ItemList) != 1 || item.request.Message.ItemList[0].TextItem == nil {
			t.Fatalf("send %d item list = %#v", index, item.request.Message.ItemList)
		}
	}
	if gotSends[0].request.Message.ItemList[0].TextItem.Text != "side message" || gotSends[1].request.Message.ItemList[0].TextItem.Text != "final answer" {
		t.Fatalf("send contents = %#v", gotSends)
	}
	if len(gotTypings) != 2 || gotTypings[0].request.Status != typingStatusActive || gotTypings[1].request.Status != typingStatusCancel {
		t.Fatalf("typing requests = %#v", gotTypings)
	}
	for index, item := range gotTypings {
		if item.request.WeChatUserID != "user-a" || item.request.TypingTicket != "ticket-a" {
			t.Fatalf("typing %d target = %#v", index, item.request)
		}
		if item.request.BaseInfo.ChannelVersion != "2.4.6" || item.request.BaseInfo.BotAgent != botAgent {
			t.Fatalf("typing %d base_info = %#v", index, item.request.BaseInfo)
		}
		if item.headers.Get("Authorization") != "Bearer secret-token" {
			t.Fatalf("typing %d auth headers = %#v", index, item.headers)
		}
	}
	cursor, err := os.ReadFile(cursorFile)
	if err != nil || strings.TrimSpace(string(cursor)) != "cursor-1" {
		t.Fatalf("cursor = %q, err = %v", cursor, err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("channel did not stop")
	}
}

func TestChannelStartsTypingBeforeGraphAndCachesTicket(t *testing.T) {
	var mu sync.Mutex
	var events []string
	getConfigCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/ilink/bot/getconfig":
			mu.Lock()
			getConfigCalls++
			mu.Unlock()
			_ = json.NewEncoder(response).Encode(map[string]any{"ret": 0, "typing_ticket": "ticket"})
		case "/ilink/bot/sendtyping":
			var payload sendTypingRequest
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode sendtyping request: %v", err)
				return
			}
			event := "typing-active"
			if payload.Status == typingStatusCancel {
				event = "typing-cancel"
			}
			mu.Lock()
			events = append(events, event)
			mu.Unlock()
			_ = json.NewEncoder(response).Encode(map[string]any{"ret": 0})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	channel := &Channel{
		config:          Config{BotToken: "token", BaseURL: server.URL},
		client:          server.Client(),
		logger:          discardLogger(),
		wechatUIN:       randomWeChatUIN(),
		typingKeepalive: time.Hour,
	}
	channel.handler = chatchannel.HandlerFunc(func(context.Context, chatchannel.InboundMessage, chatcap.ReplySink) error {
		mu.Lock()
		events = append(events, "graph")
		mu.Unlock()
		return nil
	})
	for index := range 2 {
		if err := channel.handleMessage(context.Background(), weixinMessage{
			MessageID:    json.RawMessage(strconv.Itoa(index + 1)),
			FromUserID:   "user",
			ContextToken: "ctx",
			MessageType:  1,
			ItemList:     []messageItem{{Type: 1, TextItem: &textItem{Text: "hello"}}},
		}); err != nil {
			t.Fatal(err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if getConfigCalls != 1 {
		t.Fatalf("getconfig calls = %d", getConfigCalls)
	}
	wantEvents := []string{"typing-active", "graph", "typing-cancel", "typing-active", "graph", "typing-cancel"}
	if !slices.Equal(events, wantEvents) {
		t.Fatalf("events = %#v, want %#v", events, wantEvents)
	}
}

func TestChannelKeepsTypingActiveWhileGraphRuns(t *testing.T) {
	typingStatuses := make(chan int, 4)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/ilink/bot/getconfig":
			_ = json.NewEncoder(response).Encode(map[string]any{"ret": 0, "typing_ticket": "ticket"})
		case "/ilink/bot/sendtyping":
			var payload sendTypingRequest
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode sendtyping request: %v", err)
				return
			}
			typingStatuses <- payload.Status
			_ = json.NewEncoder(response).Encode(map[string]any{"ret": 0})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	channel := &Channel{
		config:          Config{BotToken: "token", BaseURL: server.URL},
		client:          server.Client(),
		logger:          discardLogger(),
		wechatUIN:       randomWeChatUIN(),
		typingKeepalive: 10 * time.Millisecond,
	}
	channel.handler = chatchannel.HandlerFunc(func(context.Context, chatchannel.InboundMessage, chatcap.ReplySink) error {
		for index := range 2 {
			select {
			case status := <-typingStatuses:
				if status != typingStatusActive {
					t.Fatalf("typing status %d = %d", index, status)
				}
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for typing status")
			}
		}
		return nil
	})
	if err := channel.handleMessage(context.Background(), weixinMessage{
		MessageID:    json.RawMessage(`1`),
		FromUserID:   "user",
		ContextToken: "ctx",
		MessageType:  1,
		ItemList:     []messageItem{{Type: 1, TextItem: &textItem{Text: "hello"}}},
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(time.Second)
	for {
		select {
		case status := <-typingStatuses:
			if status == typingStatusCancel {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for typing cancel")
		}
	}
}

func TestChannelSendsUnsupportedAndFailureMessages(t *testing.T) {
	var mu sync.Mutex
	var contents []string
	var typingStatuses []int
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/ilink/bot/getconfig":
			_ = json.NewEncoder(response).Encode(map[string]any{"ret": 0, "typing_ticket": "ticket"})
		case "/ilink/bot/sendtyping":
			var payload sendTypingRequest
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode sendtyping request: %v", err)
				return
			}
			mu.Lock()
			typingStatuses = append(typingStatuses, payload.Status)
			mu.Unlock()
			_ = json.NewEncoder(response).Encode(map[string]any{"ret": 0})
		case "/ilink/bot/sendmessage":
			var payload sendMessageRequest
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode sendmessage request: %v", err)
				return
			}
			mu.Lock()
			contents = append(contents, payload.Message.ItemList[0].TextItem.Text)
			mu.Unlock()
			_ = json.NewEncoder(response).Encode(map[string]any{"ret": 0})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	channel := &Channel{
		config: Config{BotToken: "token", BaseURL: server.URL, UnsupportedMessage: "unsupported", FailureMessage: "failed"},
		client: server.Client(),
		logger: discardLogger(),
		handler: chatchannel.HandlerFunc(func(context.Context, chatchannel.InboundMessage, chatcap.ReplySink) error {
			return errors.New("graph failed")
		}),
		wechatUIN: randomWeChatUIN(),
	}
	if err := channel.handleMessage(context.Background(), weixinMessage{FromUserID: "user", ContextToken: "ctx", MessageType: 2}); err != nil {
		t.Fatal(err)
	}
	if err := channel.handleMessage(context.Background(), weixinMessage{
		FromUserID:   "user",
		ContextToken: "ctx",
		MessageType:  1,
		ItemList:     []messageItem{{Type: 1, TextItem: &textItem{Text: "hello"}}},
	}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(contents) != 2 || contents[0] != "unsupported" || contents[1] != "failed" {
		t.Fatalf("contents = %#v", contents)
	}
	if len(typingStatuses) != 2 || typingStatuses[0] != typingStatusActive || typingStatuses[1] != typingStatusCancel {
		t.Fatalf("typing statuses = %#v", typingStatuses)
	}
}

func TestChannelRunsGraphWhenTypingIsUnavailable(t *testing.T) {
	var handlerCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/ilink/bot/getconfig":
			response.WriteHeader(http.StatusServiceUnavailable)
		case "/ilink/bot/sendmessage":
			_ = json.NewEncoder(response).Encode(map[string]any{"ret": 0})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	channel := &Channel{
		config: Config{BotToken: "token", BaseURL: server.URL},
		client: server.Client(),
		logger: discardLogger(),
		handler: chatchannel.HandlerFunc(func(ctx context.Context, _ chatchannel.InboundMessage, sink chatcap.ReplySink) error {
			handlerCalled = true
			return sink.Emit(ctx, chatcap.Reply{Kind: chatcap.ReplyFinish, Content: "reply"})
		}),
		wechatUIN: randomWeChatUIN(),
	}
	if err := channel.handleMessage(context.Background(), weixinMessage{
		MessageID:    json.RawMessage(`123`),
		FromUserID:   "user",
		ContextToken: "ctx",
		MessageType:  1,
		ItemList:     []messageItem{{Type: 1, TextItem: &textItem{Text: "hello"}}},
	}); err != nil {
		t.Fatal(err)
	}
	if !handlerCalled {
		t.Fatal("graph handler was not called")
	}
}

func TestStopTypingUsesContextAfterGraphCancellation(t *testing.T) {
	var status int
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/ilink/bot/sendtyping" {
			http.NotFound(response, request)
			return
		}
		var payload sendTypingRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode sendtyping request: %v", err)
			return
		}
		status = payload.Status
		_ = json.NewEncoder(response).Encode(map[string]any{"ret": 0})
	}))
	defer server.Close()

	channel := &Channel{
		config:    Config{BotToken: "token", BaseURL: server.URL},
		client:    server.Client(),
		logger:    discardLogger(),
		wechatUIN: randomWeChatUIN(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	channel.stopTyping(ctx, "message", "user", "ticket")
	if status != typingStatusCancel {
		t.Fatalf("typing status = %d", status)
	}
}

func TestChannelStopsOnInvalidToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/ilink/bot/getupdates" {
			http.NotFound(response, request)
			return
		}
		_ = json.NewEncoder(response).Encode(map[string]any{"ret": -14, "errmsg": "expired token"})
	}))
	defer server.Close()

	instance, err := (Factory{HTTPClient: server.Client()}).New(chatchannel.InstanceConfig{
		TriggerID: "invalid-token",
		Handler:   chatchannel.HandlerFunc(func(context.Context, chatchannel.InboundMessage, chatcap.ReplySink) error { return nil }),
		Config:    map[string]any{"bot_token": "token", "base_url": server.URL, "cursor_file": filepathForTest(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = instance.Run(context.Background())
	var tokenErr *tokenError
	if !errors.As(err, &tokenErr) {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestSendTextRetriesTransientFailure(t *testing.T) {
	var logOutput bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logOutput, &slog.HandlerOptions{Level: slog.LevelDebug}))
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls++
		if calls == 1 {
			response.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(response).Encode(map[string]any{"ret": 0})
	}))
	defer server.Close()

	channel := &Channel{
		config:    Config{BotToken: "private-bot-token", BaseURL: server.URL},
		client:    server.Client(),
		logger:    logger,
		wechatUIN: randomWeChatUIN(),
	}
	if err := channel.sendText(context.Background(), "private-recipient", "private-context-token", "private-reply-content"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("sendmessage calls = %d", calls)
	}
	logs := logOutput.String()
	for _, level := range []string{"level=DEBUG", "level=INFO", "level=ERROR"} {
		if !strings.Contains(logs, level) {
			t.Fatalf("logs do not contain %s: %s", level, logs)
		}
	}
	for _, sensitive := range []string{"private-bot-token", "private-recipient", "private-context-token", "private-reply-content"} {
		if strings.Contains(logs, sensitive) {
			t.Fatalf("logs contain sensitive value %q: %s", sensitive, logs)
		}
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func filepathForTest(t *testing.T) string {
	t.Helper()
	return t.TempDir() + "/cursor.sync"
}
