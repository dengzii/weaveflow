package wecom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	chatcap "github.com/dengzii/weaveflow/capability/chat"
	"github.com/dengzii/weaveflow/internal/chatchannel"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	ChannelID                 = "wecom"
	DefaultEndpoint           = "wss://openws.work.weixin.qq.com"
	DefaultWelcomeMessage     = "请输入消息。"
	DefaultUnsupportedMessage = "当前仅支持文本消息。"
	DefaultFailureMessage     = "消息处理失败，请稍后重试。"
	heartbeatInterval         = 30 * time.Second
	subscribeTimeout          = 15 * time.Second
	writeTimeout              = 10 * time.Second
	replyAckTimeout           = 5 * time.Second
	initialReconnectDelay     = time.Second
	maxReconnectDelay         = 30 * time.Second
	maxMessageSize            = 16 << 20
	maxStreamContentBytes     = 20480
	chatInvocationTimeout     = 10 * time.Minute
)

type Config struct {
	BotID              string
	Secret             string
	Endpoint           string
	WelcomeMessage     string
	UnsupportedMessage string
	FailureMessage     string
}

type Factory struct {
	Logger     *slog.Logger
	Dialer     *websocket.Dialer
	ackTimeout time.Duration
}

func Register(registry *chatchannel.Registry) error {
	return registry.Register(Factory{})
}

func (Factory) Definition() chatchannel.Definition {
	return chatchannel.Definition{
		ID:          ChannelID,
		Title:       "WeCom Intelligent Bot",
		Description: "Connect to a WeCom intelligent bot through the API-mode long connection.",
		ConfigSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"bot_id": map[string]any{
					"type":        "string",
					"title":       "Bot ID",
					"description": "WeCom intelligent bot ID.",
				},
				"secret": map[string]any{
					"type":        "string",
					"title":       "Secret",
					"description": "WeCom API-mode long-connection secret.",
					"format":      "password",
					"writeOnly":   true,
				},
				"endpoint": map[string]any{
					"type":        "string",
					"title":       "WebSocket endpoint",
					"default":     DefaultEndpoint,
					"description": "Override only for a compatible WeCom gateway.",
				},
				"welcome_message": map[string]any{
					"type":    "string",
					"title":   "Welcome message",
					"default": DefaultWelcomeMessage,
				},
				"unsupported_message": map[string]any{
					"type":    "string",
					"title":   "Unsupported message",
					"default": DefaultUnsupportedMessage,
				},
				"failure_message": map[string]any{
					"type":    "string",
					"title":   "Failure message",
					"default": DefaultFailureMessage,
				},
			},
			"required":             []any{"bot_id", "secret"},
			"additionalProperties": false,
		},
	}
}

func (Factory) ValidateConfig(raw map[string]any) error {
	_, err := ParseConfig(raw)
	return err
}

func (factory Factory) New(instance chatchannel.InstanceConfig) (chatchannel.Instance, error) {
	config, err := ParseConfig(instance.Config)
	if err != nil {
		return nil, err
	}
	logger := factory.Logger
	if logger == nil {
		logger = slog.Default()
	}
	triggerID := strings.TrimSpace(instance.TriggerID)
	logger = logger.With(
		"component", "chat_channel",
		"channel", ChannelID,
		"trigger_id", triggerID,
	)
	dialer := factory.Dialer
	if dialer == nil {
		dialer = websocket.DefaultDialer
	}
	ackTimeout := factory.ackTimeout
	if ackTimeout <= 0 {
		ackTimeout = replyAckTimeout
	}
	return &Channel{
		triggerID:  triggerID,
		config:     config,
		handler:    instance.Handler,
		logger:     logger,
		dialer:     dialer,
		ackTimeout: ackTimeout,
	}, nil
}

func ParseConfig(raw map[string]any) (Config, error) {
	allowed := map[string]struct{}{
		"bot_id": {}, "secret": {}, "endpoint": {}, "welcome_message": {},
		"unsupported_message": {}, "failure_message": {},
	}
	for key := range raw {
		if _, ok := allowed[key]; !ok {
			return Config{}, fmt.Errorf("unsupported config field %q", key)
		}
	}
	config := Config{
		BotID:              configString(raw, "bot_id"),
		Secret:             configString(raw, "secret"),
		Endpoint:           configString(raw, "endpoint"),
		WelcomeMessage:     configString(raw, "welcome_message"),
		UnsupportedMessage: configString(raw, "unsupported_message"),
		FailureMessage:     configString(raw, "failure_message"),
	}
	if config.Endpoint == "" {
		config.Endpoint = DefaultEndpoint
	}
	if config.WelcomeMessage == "" {
		config.WelcomeMessage = DefaultWelcomeMessage
	}
	if config.UnsupportedMessage == "" {
		config.UnsupportedMessage = DefaultUnsupportedMessage
	}
	if config.FailureMessage == "" {
		config.FailureMessage = DefaultFailureMessage
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (config Config) Validate() error {
	if strings.TrimSpace(config.BotID) == "" {
		return errors.New("bot_id is required")
	}
	if strings.TrimSpace(config.Secret) == "" {
		return errors.New("secret is required")
	}
	endpoint, err := url.Parse(strings.TrimSpace(config.Endpoint))
	if err != nil {
		return fmt.Errorf("parse endpoint: %w", err)
	}
	if endpoint.Host == "" || (endpoint.Scheme != "ws" && endpoint.Scheme != "wss") {
		return fmt.Errorf("endpoint must be an absolute ws:// or wss:// URL: %q", config.Endpoint)
	}
	return nil
}

func configString(raw map[string]any, key string) string {
	value, _ := raw[key].(string)
	return strings.TrimSpace(value)
}

type Channel struct {
	triggerID  string
	config     Config
	handler    chatchannel.Handler
	logger     *slog.Logger
	dialer     *websocket.Dialer
	ackTimeout time.Duration
}

func (channel *Channel) Run(ctx context.Context) error {
	if channel == nil || channel.handler == nil {
		return errors.New("WeCom channel handler is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	channel.logger.Info("WeCom channel starting")
	defer channel.logger.Info("WeCom channel stopped")
	reconnectDelay := initialReconnectDelay
	for {
		channel.logger.Debug("WeCom channel connecting")
		conn, _, err := channel.dialer.DialContext(ctx, channel.config.Endpoint, nil)
		if err != nil {
			if ctx.Err() != nil {
				channel.logger.Debug("WeCom connection attempt canceled")
				return nil
			}
			channel.logger.Error("WeCom channel connection failed", "error", err, "retry_in", reconnectDelay)
			if err := waitForReconnect(ctx, reconnectDelay); err != nil {
				return nil
			}
			reconnectDelay = nextReconnectDelay(reconnectDelay)
			continue
		}

		channel.logger.Info("WeCom channel connected")
		err = channel.serveConnection(ctx, conn)
		if ctx.Err() != nil {
			return nil
		}
		var subscriptionRejected *subscriptionRejectedError
		var connectionReplaced *connectionReplacedError
		if errors.As(err, &subscriptionRejected) || errors.As(err, &connectionReplaced) {
			channel.logger.Error("WeCom channel terminated by platform", "error", err)
			return err
		}
		channel.logger.Error("WeCom channel connection closed", "error", err, "retry_in", reconnectDelay)
		if err := waitForReconnect(ctx, reconnectDelay); err != nil {
			return nil
		}
		reconnectDelay = nextReconnectDelay(reconnectDelay)
	}
}

type headers struct {
	RequestID string `json:"req_id"`
}

type incomingFrame struct {
	Command   string          `json:"cmd"`
	Headers   headers         `json:"headers"`
	Body      json.RawMessage `json:"body"`
	ErrorCode *int            `json:"errcode,omitempty"`
	ErrorMsg  string          `json:"errmsg,omitempty"`
}

type outgoingFrame struct {
	Command string  `json:"cmd"`
	Headers headers `json:"headers"`
	Body    any     `json:"body,omitempty"`
}

type callbackBody struct {
	MessageID   string `json:"msgid"`
	MessageType string `json:"msgtype"`
	ChatID      string `json:"chatid"`
	From        struct {
		UserID string `json:"userid"`
	} `json:"from"`
	Text struct {
		Content string `json:"content"`
	} `json:"text"`
	Event struct {
		EventType string `json:"eventtype"`
	} `json:"event"`
}

type subscribeBody struct {
	BotID  string `json:"bot_id"`
	Secret string `json:"secret"`
}

type streamReplyBody struct {
	MessageType string `json:"msgtype"`
	Stream      struct {
		ID      string `json:"id"`
		Finish  bool   `json:"finish"`
		Content string `json:"content"`
	} `json:"stream"`
}

type welcomeReplyBody struct {
	MessageType string `json:"msgtype"`
	Text        struct {
		Content string `json:"content"`
	} `json:"text"`
}

type subscriptionRejectedError struct {
	Code    int
	Message string
}

func (err *subscriptionRejectedError) Error() string {
	return fmt.Sprintf("subscription rejected: errcode=%d errmsg=%s", err.Code, err.Message)
}

type connectionReplacedError struct{}

func (*connectionReplacedError) Error() string {
	return "connection replaced by another client for the same bot"
}

func (channel *Channel) serveConnection(ctx context.Context, conn *websocket.Conn) error {
	defer conn.Close()
	conn.SetReadLimit(maxMessageSize)
	if err := subscribe(conn, channel.config); err != nil {
		return err
	}
	channel.logger.Info("WeCom channel subscription accepted")
	writer := newConnectionWriter(conn, channel.ackTimeout, channel.logger)
	defer writer.Close(errors.New("WeCom connection closed"))
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	messages := make(chan []byte, 1)
	readErrors := make(chan error, 1)
	workerErrors := make(chan error, 1)
	go readMessages(sessionCtx, conn, writer, messages, readErrors)

	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "shutdown"), time.Now().Add(time.Second))
			return nil
		case err := <-readErrors:
			return err
		case err := <-workerErrors:
			return err
		case payload := <-messages:
			if err := channel.handleIncomingFrame(sessionCtx, writer, payload, workerErrors); err != nil {
				return err
			}
		case <-heartbeat.C:
			channel.logger.Debug("WeCom channel sending heartbeat")
			if err := writer.WriteFrame(outgoingFrame{Command: "ping", Headers: headers{RequestID: uuid.NewString()}}); err != nil {
				return fmt.Errorf("send heartbeat: %w", err)
			}
		}
	}
}

func subscribe(conn *websocket.Conn, config Config) error {
	requestID := uuid.NewString()
	if err := writeFrame(conn, outgoingFrame{
		Command: "aibot_subscribe",
		Headers: headers{RequestID: requestID},
		Body:    subscribeBody{BotID: config.BotID, Secret: config.Secret},
	}); err != nil {
		return fmt.Errorf("send subscription: %w", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(subscribeTimeout)); err != nil {
		return fmt.Errorf("set subscription deadline: %w", err)
	}
	defer conn.SetReadDeadline(time.Time{})
	var response incomingFrame
	if err := conn.ReadJSON(&response); err != nil {
		return fmt.Errorf("read subscription response: %w", err)
	}
	if response.Headers.RequestID != requestID {
		return fmt.Errorf("subscription response req_id mismatch: got %q", response.Headers.RequestID)
	}
	if response.ErrorCode == nil {
		return errors.New("subscription response is missing errcode")
	}
	if *response.ErrorCode != 0 {
		return &subscriptionRejectedError{Code: *response.ErrorCode, Message: response.ErrorMsg}
	}
	return nil
}

func readMessages(ctx context.Context, conn *websocket.Conn, writer *connectionWriter, messages chan<- []byte, readErrors chan<- error) {
	for {
		_, payload, err := conn.ReadMessage()
		if err != nil {
			select {
			case readErrors <- err:
			default:
			}
			return
		}
		if writer.HandleResponse(payload) {
			continue
		}
		select {
		case messages <- payload:
		case <-ctx.Done():
			return
		}
	}
}

type frameWriter interface {
	WriteFrame(outgoingFrame) error
	WriteReply(context.Context, outgoingFrame) error
}

type replyResult struct {
	frame incomingFrame
	err   error
}

type pendingReply struct {
	result chan replyResult
}

type requestGate struct {
	token chan struct{}
	refs  int
}

type connectionWriter struct {
	writeMu    sync.Mutex
	pendingMu  sync.Mutex
	conn       *websocket.Conn
	logger     *slog.Logger
	ackTimeout time.Duration
	pending    map[string]*pendingReply
	gates      map[string]*requestGate
	done       chan struct{}
	closedErr  error
}

func newConnectionWriter(conn *websocket.Conn, ackTimeout time.Duration, logger *slog.Logger) *connectionWriter {
	if ackTimeout <= 0 {
		ackTimeout = replyAckTimeout
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &connectionWriter{
		conn:       conn,
		logger:     logger,
		ackTimeout: ackTimeout,
		pending:    make(map[string]*pendingReply),
		gates:      make(map[string]*requestGate),
		done:       make(chan struct{}),
	}
}

func (writer *connectionWriter) WriteFrame(frame outgoingFrame) error {
	if writer == nil || writer.conn == nil {
		return errors.New("WeCom connection is unavailable")
	}
	writer.writeMu.Lock()
	defer writer.writeMu.Unlock()
	return writeFrame(writer.conn, frame)
}

func (writer *connectionWriter) WriteReply(ctx context.Context, frame outgoingFrame) error {
	if writer == nil || writer.conn == nil {
		return errors.New("WeCom connection is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestID := strings.TrimSpace(frame.Headers.RequestID)
	if requestID == "" {
		writer.logger.Error("WeCom reply rejected before send", "reason", "missing_request_id", "command", frame.Command)
		return errors.New("WeCom reply req_id is required")
	}
	release, err := writer.acquireRequestGate(ctx, requestID)
	if err != nil {
		return err
	}
	defer release()

	pending := &pendingReply{result: make(chan replyResult, 1)}
	writer.pendingMu.Lock()
	if writer.closedErr != nil {
		err := writer.closedErr
		writer.pendingMu.Unlock()
		return err
	}
	writer.pending[requestID] = pending
	writer.pendingMu.Unlock()
	defer writer.removePending(requestID, pending)

	finalReply := false
	if body, ok := frame.Body.(streamReplyBody); ok {
		finalReply = body.Stream.Finish
	}
	writer.logger.Debug("WeCom reply sending", "request_id", requestID, "command", frame.Command, "final", finalReply)
	if err := writer.WriteFrame(frame); err != nil {
		writer.logger.Error("WeCom reply write failed", "request_id", requestID, "command", frame.Command, "error", err)
		return err
	}
	timer := time.NewTimer(writer.ackTimeout)
	defer timer.Stop()
	select {
	case result := <-pending.result:
		if result.err != nil {
			writer.logger.Error("WeCom reply failed before ACK", "request_id", requestID, "error", result.err)
			return result.err
		}
		if result.frame.ErrorCode == nil {
			writer.logger.Error("WeCom reply ACK was invalid", "request_id", requestID, "reason", "missing_error_code")
			return errors.New("WeCom reply ACK is missing errcode")
		}
		if *result.frame.ErrorCode != 0 {
			writer.logger.Error("WeCom reply rejected by platform",
				"request_id", requestID,
				"error_code", *result.frame.ErrorCode,
				"error_message", result.frame.ErrorMsg,
			)
			return fmt.Errorf("WeCom reply rejected: errcode=%d errmsg=%s", *result.frame.ErrorCode, result.frame.ErrorMsg)
		}
		if finalReply {
			writer.logger.Info("WeCom final reply acknowledged", "request_id", requestID)
		} else {
			writer.logger.Debug("WeCom reply acknowledged", "request_id", requestID)
		}
		return nil
	case <-timer.C:
		writer.logger.Error("WeCom reply ACK timed out", "request_id", requestID, "timeout", writer.ackTimeout)
		return fmt.Errorf("WeCom reply ACK timeout after %s for req_id %q", writer.ackTimeout, requestID)
	case <-ctx.Done():
		writer.logger.Debug("WeCom reply canceled", "request_id", requestID, "error", ctx.Err())
		return ctx.Err()
	case <-writer.done:
		err := writer.closeError()
		writer.logger.Error("WeCom reply interrupted by connection close", "request_id", requestID, "error", err)
		return err
	}
}

func (writer *connectionWriter) HandleResponse(payload []byte) bool {
	if writer == nil {
		return false
	}
	var frame incomingFrame
	if err := json.Unmarshal(payload, &frame); err != nil || frame.Command != "" {
		return false
	}
	requestID := strings.TrimSpace(frame.Headers.RequestID)
	if requestID == "" {
		return false
	}
	writer.pendingMu.Lock()
	pending := writer.pending[requestID]
	writer.pendingMu.Unlock()
	if pending == nil {
		return false
	}
	select {
	case pending.result <- replyResult{frame: frame}:
	default:
	}
	return true
}

func (writer *connectionWriter) Close(err error) {
	if writer == nil {
		return
	}
	if err == nil {
		err = errors.New("WeCom connection closed")
	}
	writer.pendingMu.Lock()
	if writer.closedErr != nil {
		writer.pendingMu.Unlock()
		return
	}
	writer.closedErr = err
	close(writer.done)
	pending := make([]*pendingReply, 0, len(writer.pending))
	for _, item := range writer.pending {
		pending = append(pending, item)
	}
	writer.pending = make(map[string]*pendingReply)
	writer.pendingMu.Unlock()
	for _, item := range pending {
		select {
		case item.result <- replyResult{err: err}:
		default:
		}
	}
}

func (writer *connectionWriter) acquireRequestGate(ctx context.Context, requestID string) (func(), error) {
	writer.pendingMu.Lock()
	if writer.closedErr != nil {
		err := writer.closedErr
		writer.pendingMu.Unlock()
		return nil, err
	}
	gate := writer.gates[requestID]
	if gate == nil {
		gate = &requestGate{token: make(chan struct{}, 1)}
		gate.token <- struct{}{}
		writer.gates[requestID] = gate
	}
	gate.refs++
	writer.pendingMu.Unlock()

	select {
	case <-gate.token:
		return func() {
			gate.token <- struct{}{}
			writer.releaseRequestGate(requestID, gate)
		}, nil
	case <-ctx.Done():
		writer.releaseRequestGate(requestID, gate)
		return nil, ctx.Err()
	case <-writer.done:
		writer.releaseRequestGate(requestID, gate)
		return nil, writer.closeError()
	}
}

func (writer *connectionWriter) releaseRequestGate(requestID string, gate *requestGate) {
	writer.pendingMu.Lock()
	gate.refs--
	if gate.refs == 0 && writer.gates[requestID] == gate {
		delete(writer.gates, requestID)
	}
	writer.pendingMu.Unlock()
}

func (writer *connectionWriter) removePending(requestID string, pending *pendingReply) {
	writer.pendingMu.Lock()
	if writer.pending[requestID] == pending {
		delete(writer.pending, requestID)
	}
	writer.pendingMu.Unlock()
}

func (writer *connectionWriter) closeError() error {
	writer.pendingMu.Lock()
	defer writer.pendingMu.Unlock()
	if writer.closedErr != nil {
		return writer.closedErr
	}
	return errors.New("WeCom connection closed")
}

func (channel *Channel) handleIncomingFrame(ctx context.Context, writer frameWriter, payload []byte, workerErrors chan<- error) error {
	var frame incomingFrame
	if err := json.Unmarshal(payload, &frame); err != nil {
		return fmt.Errorf("decode incoming frame: %w", err)
	}
	channel.logger.Debug("WeCom frame received",
		"command", frame.Command,
		"request_id", frame.Headers.RequestID,
		"payload_bytes", len(payload),
	)
	if frame.Command == "" {
		if frame.ErrorCode != nil && *frame.ErrorCode != 0 {
			channel.logger.Error("WeCom request rejected",
				"request_id", frame.Headers.RequestID,
				"error_code", *frame.ErrorCode,
				"error_message", frame.ErrorMsg,
			)
		}
		return nil
	}
	switch frame.Command {
	case "aibot_msg_callback":
		go func() {
			if err := channel.handleMessageCallback(ctx, writer, frame); err != nil {
				select {
				case workerErrors <- err:
				default:
				}
			}
		}()
		return nil
	case "aibot_event_callback":
		return channel.handleEventCallback(ctx, writer, frame)
	default:
		channel.logger.Debug("WeCom unsupported command ignored", "command", frame.Command)
		return nil
	}
}

func (channel *Channel) handleMessageCallback(ctx context.Context, writer frameWriter, frame incomingFrame) error {
	if strings.TrimSpace(frame.Headers.RequestID) == "" {
		return errors.New("message callback is missing req_id")
	}
	var body callbackBody
	if err := json.Unmarshal(frame.Body, &body); err != nil {
		return fmt.Errorf("decode message callback: %w", err)
	}
	channel.logger.Debug("WeCom message received",
		"message_id", body.MessageID,
		"message_type", body.MessageType,
		"has_chat_id", strings.TrimSpace(body.ChatID) != "",
	)
	sink := newReplySink(ctx, writer, frame.Headers.RequestID, channel.config.FailureMessage)
	if body.MessageType != "text" {
		err := sink.Emit(ctx, chatcap.Reply{Kind: chatcap.ReplyFinish, Content: channel.config.UnsupportedMessage})
		if err != nil {
			channel.logger.Error("WeCom unsupported message reply failed", "message_id", body.MessageID, "error", err)
			return err
		}
		channel.logger.Info("WeCom unsupported message handled", "message_id", body.MessageID, "message_type", body.MessageType)
		return nil
	}
	invocationCtx, cancel := context.WithTimeout(ctx, chatInvocationTimeout)
	defer cancel()
	message := chatcap.Message{
		ID:             body.MessageID,
		ConversationID: firstNonEmpty(body.ChatID, body.From.UserID, body.MessageID),
		Content:        body.Text.Content,
		Metadata: map[string]any{
			"channel":      ChannelID,
			"request_id":   frame.Headers.RequestID,
			"message_type": body.MessageType,
		},
	}
	if err := channel.handler.Handle(invocationCtx, message, sink); err != nil {
		channel.logger.Error("WeCom message trigger failed", "message_id", body.MessageID, "error", err)
		return sink.Fail(context.WithoutCancel(ctx), err)
	}
	channel.logger.Info("WeCom message handled", "message_id", body.MessageID)
	return nil
}

func (channel *Channel) handleEventCallback(ctx context.Context, writer frameWriter, frame incomingFrame) error {
	var body callbackBody
	if err := json.Unmarshal(frame.Body, &body); err != nil {
		return fmt.Errorf("decode event callback: %w", err)
	}
	channel.logger.Debug("WeCom event received", "event_type", body.Event.EventType)
	switch body.Event.EventType {
	case "enter_chat":
		if strings.TrimSpace(frame.Headers.RequestID) == "" {
			return errors.New("enter_chat event is missing req_id")
		}
		reply := welcomeReplyBody{MessageType: "text"}
		reply.Text.Content = channel.config.WelcomeMessage
		if err := writer.WriteReply(ctx, outgoingFrame{Command: "aibot_respond_welcome_msg", Headers: headers{RequestID: frame.Headers.RequestID}, Body: reply}); err != nil {
			channel.logger.Error("WeCom welcome message failed", "error", err)
			return err
		}
		channel.logger.Info("WeCom welcome message sent")
		return nil
	case "disconnected_event":
		return &connectionReplacedError{}
	default:
		channel.logger.Debug("WeCom unsupported event ignored", "event_type", body.Event.EventType)
		return nil
	}
}

type replySink struct {
	mu             sync.Mutex
	ctx            context.Context
	writer         frameWriter
	requestID      string
	failureMessage string
	streamID       string
	lastContent    string
	finished       bool
	finishing      bool
	updateInFlight bool
	pendingUpdate  string
	updateErr      error
	changed        chan struct{}
}

func newReplySink(ctx context.Context, writer frameWriter, requestID, failureMessage string) *replySink {
	if ctx == nil {
		ctx = context.Background()
	}
	return &replySink{
		ctx:            ctx,
		writer:         writer,
		requestID:      requestID,
		failureMessage: failureMessage,
		changed:        make(chan struct{}),
	}
}

func (sink *replySink) Emit(ctx context.Context, reply chatcap.Reply) error {
	if sink == nil || sink.writer == nil {
		return errors.New("WeCom reply writer is unavailable")
	}
	switch reply.Kind {
	case chatcap.ReplyUpdate:
		return sink.emitUpdate(reply.Content)
	case chatcap.ReplyMessage:
		if strings.TrimSpace(reply.Content) == "" {
			return nil
		}
		sink.mu.Lock()
		stopped := sink.finished || sink.finishing
		sink.mu.Unlock()
		if stopped {
			return nil
		}
		if err := validateStreamContent(reply.Content); err != nil {
			return err
		}
		if err := sink.waitForUpdates(ctx, false); err != nil {
			return err
		}
		return sink.sendStream(ctx, uuid.NewString(), reply.Content, true)
	case chatcap.ReplyFinish:
		return sink.finish(ctx, reply.Content, reply.Error != "")
	default:
		return fmt.Errorf("unsupported chat reply kind %q", reply.Kind)
	}
}

func (sink *replySink) Fail(ctx context.Context, _ error) error {
	message := sink.failureMessage
	if strings.TrimSpace(message) == "" {
		message = DefaultFailureMessage
	}
	return sink.finish(ctx, message, true)
}

func (sink *replySink) emitUpdate(content string) error {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	if err := validateStreamContent(content); err != nil {
		return err
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.finished || sink.finishing {
		return nil
	}
	if sink.updateErr != nil {
		return sink.updateErr
	}
	if sink.streamID == "" {
		sink.streamID = uuid.NewString()
	}
	sink.lastContent = content
	if sink.updateInFlight {
		sink.pendingUpdate = content
		return nil
	}
	sink.updateInFlight = true
	go sink.runUpdateLoop(sink.streamID, content)
	return nil
}

func (sink *replySink) runUpdateLoop(streamID, content string) {
	for {
		err := sink.sendStream(sink.ctx, streamID, content, false)
		sink.mu.Lock()
		if err != nil {
			sink.updateErr = err
			sink.pendingUpdate = ""
			sink.updateInFlight = false
			sink.signalChangedLocked()
			sink.mu.Unlock()
			return
		}
		if sink.finishing {
			sink.pendingUpdate = ""
			sink.updateInFlight = false
			sink.signalChangedLocked()
			sink.mu.Unlock()
			return
		}
		next := sink.pendingUpdate
		sink.pendingUpdate = ""
		if next == "" || next == content {
			sink.updateInFlight = false
			sink.signalChangedLocked()
			sink.mu.Unlock()
			return
		}
		content = next
		sink.mu.Unlock()
	}
}

func (sink *replySink) finish(ctx context.Context, content string, failure bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	sink.mu.Lock()
	if sink.finished {
		sink.mu.Unlock()
		return nil
	}
	sink.finishing = true
	sink.pendingUpdate = ""
	sink.mu.Unlock()

	updateErr := sink.waitForUpdates(ctx, failure)
	if updateErr != nil && !failure {
		return updateErr
	}
	sink.mu.Lock()
	if failure {
		content = sink.failureMessage
		if strings.TrimSpace(content) == "" {
			content = DefaultFailureMessage
		}
	} else if strings.TrimSpace(content) == "" {
		content = sink.lastContent
	}
	streamID := sink.streamID
	if streamID == "" && strings.TrimSpace(content) != "" {
		streamID = uuid.NewString()
	}
	if streamID == "" && strings.TrimSpace(content) == "" {
		sink.finished = true
		sink.signalChangedLocked()
		sink.mu.Unlock()
		return nil
	}
	sink.mu.Unlock()

	if err := validateStreamContent(content); err != nil {
		return err
	}
	if err := sink.sendStream(ctx, streamID, content, true); err != nil {
		return err
	}
	sink.mu.Lock()
	sink.finished = true
	sink.signalChangedLocked()
	sink.mu.Unlock()
	return nil
}

func (sink *replySink) waitForUpdates(ctx context.Context, ignoreError bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		sink.mu.Lock()
		if !sink.updateInFlight {
			err := sink.updateErr
			if ignoreError {
				sink.updateErr = nil
				err = nil
			}
			sink.mu.Unlock()
			return err
		}
		changed := sink.changed
		sink.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (sink *replySink) signalChangedLocked() {
	close(sink.changed)
	sink.changed = make(chan struct{})
}

func (sink *replySink) sendStream(ctx context.Context, streamID, content string, finish bool) error {
	reply := streamReplyBody{MessageType: "stream"}
	reply.Stream.ID = streamID
	reply.Stream.Finish = finish
	reply.Stream.Content = content
	return sink.writer.WriteReply(ctx, outgoingFrame{Command: "aibot_respond_msg", Headers: headers{RequestID: sink.requestID}, Body: reply})
}

func validateStreamContent(content string) error {
	if len([]byte(content)) > maxStreamContentBytes {
		return fmt.Errorf("WeCom stream content exceeds %d bytes", maxStreamContentBytes)
	}
	return nil
}

func writeFrame(conn *websocket.Conn, frame outgoingFrame) error {
	if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	return conn.WriteJSON(frame)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func waitForReconnect(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func nextReconnectDelay(current time.Duration) time.Duration {
	next := current * 2
	if next > maxReconnectDelay {
		return maxReconnectDelay
	}
	return next
}
