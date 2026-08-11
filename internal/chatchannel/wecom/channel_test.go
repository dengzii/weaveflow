package wecom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	chatcap "github.com/dengzii/weaveflow/capability/chat"
	"github.com/dengzii/weaveflow/internal/chatchannel"
	"github.com/gorilla/websocket"
)

func TestChannelRoutesStreamingAndMultipleReplies(t *testing.T) {
	type wireFrame struct {
		Command string          `json:"cmd"`
		Headers headers         `json:"headers"`
		Body    json.RawMessage `json:"body"`
	}
	type serverResult struct {
		subscription wireFrame
		replies      []wireFrame
		err          error
	}
	results := make(chan serverResult, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		conn, err := upgrader.Upgrade(response, request, nil)
		if err != nil {
			results <- serverResult{err: err}
			return
		}
		defer conn.Close()
		var subscription wireFrame
		if err := conn.ReadJSON(&subscription); err != nil {
			results <- serverResult{err: err}
			return
		}
		if err := conn.WriteJSON(map[string]any{"headers": map[string]string{"req_id": subscription.Headers.RequestID}, "errcode": 0}); err != nil {
			results <- serverResult{err: err}
			return
		}
		if err := conn.WriteJSON(map[string]any{
			"cmd": "aibot_msg_callback", "headers": map[string]string{"req_id": "callback-request"},
			"body": map[string]any{
				"msgid": "message-1", "msgtype": "text", "chatid": "group-1", "chattype": "group",
				"from": map[string]string{"userid": "user-1"}, "text": map[string]string{"content": "@RobotA /new"},
			},
		}); err != nil {
			results <- serverResult{err: err}
			return
		}
		replies := make([]wireFrame, 0, 3)
		for len(replies) < 3 {
			var reply wireFrame
			if err := conn.ReadJSON(&reply); err != nil {
				results <- serverResult{err: err}
				return
			}
			replies = append(replies, reply)
			if err := conn.WriteJSON(map[string]any{"headers": map[string]string{"req_id": reply.Headers.RequestID}, "errcode": 0}); err != nil {
				results <- serverResult{err: err}
				return
			}
		}
		results <- serverResult{subscription: subscription, replies: replies}
	}))
	defer server.Close()

	handler := chatchannel.HandlerFunc(func(ctx context.Context, message chatcap.InboundMessage, sink chatcap.ReplySink) error {
		if message.Content != "/new" || message.UserID != "user-1" || message.ConversationID != "group-1" ||
			message.Metadata["channel"] != ChannelID || message.Metadata["sender_id"] != "user-1" {
			return fmt.Errorf("unexpected message: %#v", message)
		}
		for _, reply := range []chatcap.Reply{
			{Kind: chatcap.ReplyUpdate, Content: "hel"},
			{Kind: chatcap.ReplyMessage, Content: "side"},
			{Kind: chatcap.ReplyFinish, Content: "hello"},
		} {
			if err := sink.Emit(ctx, reply); err != nil {
				return err
			}
		}
		return nil
	})
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	instance, err := (Factory{Logger: discardLogger()}).New(chatchannel.InstanceConfig{
		TriggerID: "chat", Handler: handler,
		Config: map[string]any{"bot_id": "bot", "secret": "secret", "endpoint": endpoint},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- instance.Run(ctx) }()

	var result serverResult
	select {
	case result = <-results:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("timed out waiting for WeCom replies")
	}
	if result.err != nil {
		cancel()
		t.Fatal(result.err)
	}
	var subscription subscribeBody
	if err := json.Unmarshal(result.subscription.Body, &subscription); err != nil {
		t.Fatal(err)
	}
	if subscription.BotID != "bot" || subscription.Secret != "secret" {
		t.Fatalf("subscription = %#v", subscription)
	}
	streamBodies := make([]streamReplyBody, len(result.replies))
	for index, reply := range result.replies {
		if reply.Command != "aibot_respond_msg" || reply.Headers.RequestID != "callback-request" {
			t.Fatalf("reply %d = %#v", index, reply)
		}
		if err := json.Unmarshal(reply.Body, &streamBodies[index]); err != nil {
			t.Fatal(err)
		}
	}
	if streamBodies[0].Stream.Content != "hel" || streamBodies[0].Stream.Finish {
		t.Fatalf("update = %#v", streamBodies[0])
	}
	if streamBodies[1].Stream.Content != "side" || !streamBodies[1].Stream.Finish {
		t.Fatalf("message = %#v", streamBodies[1])
	}
	if streamBodies[2].Stream.Content != "hello" || !streamBodies[2].Stream.Finish {
		t.Fatalf("finish = %#v", streamBodies[2])
	}
	if streamBodies[0].Stream.ID == "" || streamBodies[0].Stream.ID != streamBodies[2].Stream.ID || streamBodies[0].Stream.ID == streamBodies[1].Stream.ID {
		t.Fatalf("stream IDs = %#v", streamBodies)
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

func TestNormalizeInboundTextStripsOnlyGroupBotMention(t *testing.T) {
	for name, testCase := range map[string]struct {
		content  string
		chatType string
		expected string
	}{
		"group command":          {content: "@RobotA /new", chatType: "group", expected: "/new"},
		"group unicode spacing":  {content: " @Robot A\u2005/stop ", chatType: "group", expected: "/stop"},
		"group without mention":  {content: "/help", chatType: "group", expected: "/help"},
		"single mention content": {content: "@RobotA /new", chatType: "single", expected: "@RobotA /new"},
	} {
		t.Run(name, func(t *testing.T) {
			if actual := normalizeInboundText(testCase.content, testCase.chatType); actual != testCase.expected {
				t.Fatalf("normalizeInboundText(%q, %q) = %q, want %q", testCase.content, testCase.chatType, actual, testCase.expected)
			}
		})
	}
}

func TestDisconnectedEventStopsInsteadOfCompetingForConnection(t *testing.T) {
	channel := &Channel{config: Config{}, logger: discardLogger()}
	frame := incomingFrame{
		Command: "aibot_event_callback",
		Headers: headers{RequestID: "disconnect-request"},
		Body:    json.RawMessage(`{"msgtype":"event","event":{"eventtype":"disconnected_event"}}`),
	}
	err := channel.handleEventCallback(context.Background(), nil, frame)
	if _, ok := err.(*connectionReplacedError); !ok {
		t.Fatalf("handleEventCallback() error = %T %v", err, err)
	}
}

func TestChannelPropagatesRejectedReplyAndSendsFailure(t *testing.T) {
	type wireFrame struct {
		Command string          `json:"cmd"`
		Headers headers         `json:"headers"`
		Body    json.RawMessage `json:"body"`
	}
	type serverResult struct {
		first    wireFrame
		fallback wireFrame
		err      error
	}
	results := make(chan serverResult, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		conn, err := upgrader.Upgrade(response, request, nil)
		if err != nil {
			results <- serverResult{err: err}
			return
		}
		defer conn.Close()
		var subscription wireFrame
		if err := conn.ReadJSON(&subscription); err != nil {
			results <- serverResult{err: err}
			return
		}
		if err := conn.WriteJSON(map[string]any{"headers": map[string]string{"req_id": subscription.Headers.RequestID}, "errcode": 0}); err != nil {
			results <- serverResult{err: err}
			return
		}
		if err := conn.WriteJSON(map[string]any{
			"cmd": "aibot_msg_callback", "headers": map[string]string{"req_id": "rejected-request"},
			"body": map[string]any{"msgid": "message-2", "msgtype": "text", "text": map[string]string{"content": "hello"}},
		}); err != nil {
			results <- serverResult{err: err}
			return
		}
		var first wireFrame
		if err := conn.ReadJSON(&first); err != nil {
			results <- serverResult{err: err}
			return
		}
		if err := conn.WriteJSON(map[string]any{
			"headers": map[string]string{"req_id": first.Headers.RequestID},
			"errcode": 45009,
			"errmsg":  "rate limited",
		}); err != nil {
			results <- serverResult{err: err}
			return
		}
		var fallback wireFrame
		if err := conn.ReadJSON(&fallback); err != nil {
			results <- serverResult{err: err}
			return
		}
		if err := conn.WriteJSON(map[string]any{"headers": map[string]string{"req_id": fallback.Headers.RequestID}, "errcode": 0}); err != nil {
			results <- serverResult{err: err}
			return
		}
		results <- serverResult{first: first, fallback: fallback}
	}))
	defer server.Close()

	handlerErrors := make(chan error, 1)
	handler := chatchannel.HandlerFunc(func(ctx context.Context, _ chatcap.InboundMessage, sink chatcap.ReplySink) error {
		if err := sink.Emit(ctx, chatcap.Reply{Kind: chatcap.ReplyUpdate, Content: "partial"}); err != nil {
			return err
		}
		err := sink.Emit(ctx, chatcap.Reply{Kind: chatcap.ReplyFinish, Content: "complete"})
		handlerErrors <- err
		return err
	})
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	instance, err := (Factory{Logger: discardLogger()}).New(chatchannel.InstanceConfig{
		TriggerID: "chat", Handler: handler,
		Config: map[string]any{
			"bot_id": "bot", "secret": "secret", "endpoint": endpoint,
			"failure_message": "failed safely",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- instance.Run(ctx) }()

	select {
	case err := <-handlerErrors:
		if err == nil || !strings.Contains(err.Error(), "errcode=45009") {
			cancel()
			t.Fatalf("handler error = %v", err)
		}
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("timed out waiting for rejected ACK")
	}
	var result serverResult
	select {
	case result = <-results:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("timed out waiting for failure reply")
	}
	if result.err != nil {
		cancel()
		t.Fatal(result.err)
	}
	var firstBody, fallbackBody streamReplyBody
	if err := json.Unmarshal(result.first.Body, &firstBody); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(result.fallback.Body, &fallbackBody); err != nil {
		t.Fatal(err)
	}
	if firstBody.Stream.Content != "partial" || firstBody.Stream.Finish {
		t.Fatalf("first reply = %#v", firstBody)
	}
	if fallbackBody.Stream.Content != "failed safely" || !fallbackBody.Stream.Finish {
		t.Fatalf("fallback reply = %#v", fallbackBody)
	}
	if firstBody.Stream.ID != fallbackBody.Stream.ID {
		t.Fatalf("stream IDs differ: %q != %q", firstBody.Stream.ID, fallbackBody.Stream.ID)
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

func TestChannelReplyAckTimeoutSendsFailure(t *testing.T) {
	type wireFrame struct {
		Command string          `json:"cmd"`
		Headers headers         `json:"headers"`
		Body    json.RawMessage `json:"body"`
	}
	type serverResult struct {
		fallback wireFrame
		err      error
	}
	results := make(chan serverResult, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		conn, err := upgrader.Upgrade(response, request, nil)
		if err != nil {
			results <- serverResult{err: err}
			return
		}
		defer conn.Close()
		var subscription wireFrame
		if err := conn.ReadJSON(&subscription); err != nil {
			results <- serverResult{err: err}
			return
		}
		if err := conn.WriteJSON(map[string]any{"headers": map[string]string{"req_id": subscription.Headers.RequestID}, "errcode": 0}); err != nil {
			results <- serverResult{err: err}
			return
		}
		if err := conn.WriteJSON(map[string]any{
			"cmd": "aibot_msg_callback", "headers": map[string]string{"req_id": "timeout-request"},
			"body": map[string]any{"msgid": "message-3", "msgtype": "text", "text": map[string]string{"content": "hello"}},
		}); err != nil {
			results <- serverResult{err: err}
			return
		}
		var ignored wireFrame
		if err := conn.ReadJSON(&ignored); err != nil {
			results <- serverResult{err: err}
			return
		}
		var fallback wireFrame
		if err := conn.ReadJSON(&fallback); err != nil {
			results <- serverResult{err: err}
			return
		}
		if err := conn.WriteJSON(map[string]any{"headers": map[string]string{"req_id": fallback.Headers.RequestID}, "errcode": 0}); err != nil {
			results <- serverResult{err: err}
			return
		}
		results <- serverResult{fallback: fallback}
	}))
	defer server.Close()

	handlerErrors := make(chan error, 1)
	handler := chatchannel.HandlerFunc(func(ctx context.Context, _ chatcap.InboundMessage, sink chatcap.ReplySink) error {
		if err := sink.Emit(ctx, chatcap.Reply{Kind: chatcap.ReplyUpdate, Content: "partial"}); err != nil {
			return err
		}
		err := sink.Emit(ctx, chatcap.Reply{Kind: chatcap.ReplyFinish, Content: "complete"})
		handlerErrors <- err
		return err
	})
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	instance, err := (Factory{Logger: discardLogger(), ackTimeout: 50 * time.Millisecond}).New(chatchannel.InstanceConfig{
		TriggerID: "chat", Handler: handler,
		Config: map[string]any{
			"bot_id": "bot", "secret": "secret", "endpoint": endpoint,
			"failure_message": "timed out safely",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- instance.Run(ctx) }()

	select {
	case err := <-handlerErrors:
		if err == nil || !strings.Contains(err.Error(), "ACK timeout") {
			cancel()
			t.Fatalf("handler error = %v", err)
		}
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("timed out waiting for ACK timeout")
	}
	var result serverResult
	select {
	case result = <-results:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("timed out waiting for timeout fallback")
	}
	if result.err != nil {
		cancel()
		t.Fatal(result.err)
	}
	var fallbackBody streamReplyBody
	if err := json.Unmarshal(result.fallback.Body, &fallbackBody); err != nil {
		t.Fatal(err)
	}
	if fallbackBody.Stream.Content != "timed out safely" || !fallbackBody.Stream.Finish {
		t.Fatalf("fallback reply = %#v", fallbackBody)
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

type controlledFrameWriter struct {
	frames   chan outgoingFrame
	releases chan error
}

func newControlledFrameWriter() *controlledFrameWriter {
	return &controlledFrameWriter{
		frames:   make(chan outgoingFrame, 8),
		releases: make(chan error, 8),
	}
}

func (writer *controlledFrameWriter) WriteFrame(frame outgoingFrame) error {
	return writer.WriteReply(context.Background(), frame)
}

func (writer *controlledFrameWriter) WriteReply(ctx context.Context, frame outgoingFrame) error {
	select {
	case writer.frames <- frame:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-writer.releases:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestChannelLogsDebugInfoAndErrorWithoutSensitiveValues(t *testing.T) {
	var logOutput bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logOutput, &slog.HandlerOptions{Level: slog.LevelDebug}))
	channel := &Channel{
		config: Config{
			Secret:         "private-wecom-secret",
			WelcomeMessage: "private-welcome-message",
		},
		logger: logger,
	}
	workerErrors := make(chan error, 1)
	if err := channel.handleIncomingFrame(
		context.Background(),
		newControlledFrameWriter(),
		[]byte(`{"cmd":"unsupported_command","headers":{"req_id":"request-a"}}`),
		workerErrors,
	); err != nil {
		t.Fatal(err)
	}
	if err := channel.handleIncomingFrame(
		context.Background(),
		newControlledFrameWriter(),
		[]byte(`{"headers":{"req_id":"request-b"},"errcode":45009,"errmsg":"rate limited"}`),
		workerErrors,
	); err != nil {
		t.Fatal(err)
	}
	writer := newControlledFrameWriter()
	writer.releases <- nil
	if err := channel.handleEventCallback(context.Background(), writer, incomingFrame{
		Headers: headers{RequestID: "request-c"},
		Body:    json.RawMessage(`{"event":{"eventtype":"enter_chat"}}`),
	}); err != nil {
		t.Fatal(err)
	}

	logs := logOutput.String()
	for _, level := range []string{"level=DEBUG", "level=INFO", "level=ERROR"} {
		if !strings.Contains(logs, level) {
			t.Fatalf("logs do not contain %s: %s", level, logs)
		}
	}
	for _, sensitive := range []string{"private-wecom-secret", "private-welcome-message"} {
		if strings.Contains(logs, sensitive) {
			t.Fatalf("logs contain sensitive value %q: %s", sensitive, logs)
		}
	}
}

func TestReplySinkCoalescesUpdatesAndAlwaysFinishes(t *testing.T) {
	writer := newControlledFrameWriter()
	sink := newReplySink(context.Background(), writer, "request", DefaultFailureMessage)
	if err := sink.Emit(context.Background(), chatcap.Reply{Kind: chatcap.ReplyUpdate, Content: "one"}); err != nil {
		t.Fatal(err)
	}
	first := waitForOutgoingFrame(t, writer.frames)
	if err := sink.Emit(context.Background(), chatcap.Reply{Kind: chatcap.ReplyUpdate, Content: "two"}); err != nil {
		t.Fatal(err)
	}
	if err := sink.Emit(context.Background(), chatcap.Reply{Kind: chatcap.ReplyUpdate, Content: "three"}); err != nil {
		t.Fatal(err)
	}
	writer.releases <- nil
	second := waitForOutgoingFrame(t, writer.frames)
	writer.releases <- nil

	finishDone := make(chan error, 1)
	go func() {
		finishDone <- sink.Emit(context.Background(), chatcap.Reply{Kind: chatcap.ReplyFinish, Content: "three"})
	}()
	final := waitForOutgoingFrame(t, writer.frames)
	writer.releases <- nil
	if err := <-finishDone; err != nil {
		t.Fatal(err)
	}

	firstBody := first.Body.(streamReplyBody)
	secondBody := second.Body.(streamReplyBody)
	finalBody := final.Body.(streamReplyBody)
	if firstBody.Stream.Content != "one" || secondBody.Stream.Content != "three" {
		t.Fatalf("updates = %#v, %#v", firstBody, secondBody)
	}
	if finalBody.Stream.Content != "three" || !finalBody.Stream.Finish {
		t.Fatalf("final = %#v", finalBody)
	}
	if firstBody.Stream.ID != secondBody.Stream.ID || firstBody.Stream.ID != finalBody.Stream.ID {
		t.Fatalf("stream IDs differ: %#v %#v %#v", firstBody, secondBody, finalBody)
	}
	select {
	case extra := <-writer.frames:
		t.Fatalf("unexpected extra frame: %#v", extra)
	default:
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestValidateStreamContentUsesUTF8ByteLimit(t *testing.T) {
	valid := strings.Repeat("界", maxStreamContentBytes/3)
	if err := validateStreamContent(valid); err != nil {
		t.Fatalf("valid content error = %v", err)
	}
	if err := validateStreamContent(valid + "界"); err == nil || !strings.Contains(err.Error(), "20480") {
		t.Fatalf("oversized content error = %v", err)
	}
}

func waitForOutgoingFrame(t *testing.T, frames <-chan outgoingFrame) outgoingFrame {
	t.Helper()
	select {
	case frame := <-frames:
		return frame
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for outgoing frame")
		return outgoingFrame{}
	}
}

func TestParseConfigRequiresCredentialsAndAppliesDefaults(t *testing.T) {
	if _, err := ParseConfig(map[string]any{"bot_id": "bot"}); err == nil || !strings.Contains(err.Error(), "secret") {
		t.Fatalf("missing secret error = %v", err)
	}
	config, err := ParseConfig(map[string]any{"bot_id": "bot", "secret": "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if config.Endpoint != DefaultEndpoint || config.FailureMessage != DefaultFailureMessage {
		t.Fatalf("config defaults = %#v", config)
	}
}
