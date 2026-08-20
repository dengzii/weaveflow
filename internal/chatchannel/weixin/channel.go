package weixin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	chatcap "github.com/dengzii/weaveflow/capability/chat"
	"github.com/dengzii/weaveflow/internal/chatchannel"
	"github.com/google/uuid"
)

const (
	ChannelID                 = "weixin"
	DefaultBaseURL            = "https://ilinkai.weixin.qq.com"
	DefaultUnsupportedMessage = "当前仅支持文本消息。"
	DefaultFailureMessage     = "消息处理失败，请稍后重试。"
	DefaultCursorDirectory    = ".local/weaveflow/weixin"
	apiClientVersion          = "132102"
	apiID                     = "bot"
	botAgent                  = "OpenClaw"
	pollTimeout               = 40 * time.Second
	chatInvocationTimeout     = 10 * time.Minute
	reconnectDelay            = time.Second
	sendRetryDelay            = 200 * time.Millisecond
	sendRetryAttempts         = 3
	typingRequestTimeout      = 10 * time.Second
	typingKeepaliveInterval   = 5 * time.Second
	typingTicketTTL           = 24 * time.Hour
	typingStatusActive        = 1
	typingStatusCancel        = 2
	maxMessageSize            = 16 << 20
)

type Config struct {
	BotToken           string
	BaseURL            string
	CursorFile         string
	UnsupportedMessage string
	FailureMessage     string
}

type Factory struct {
	Logger          *slog.Logger
	HTTPClient      *http.Client
	CursorDirectory string
	setupBaseURL    string
}

func Register(registry *chatchannel.Registry) error {
	return registry.Register(Factory{})
}

func RegisterWithCursorDirectory(registry *chatchannel.Registry, cursorDirectory string) error {
	return registry.Register(Factory{
		CursorDirectory: strings.TrimSpace(cursorDirectory),
	})
}

func (Factory) Definition() chatchannel.Definition {
	return chatchannel.Definition{
		ID:          ChannelID,
		Title:       "WeChat Bot",
		Description: "Connect directly to a Tencent WeChat iLink bot through the official HTTP API.",
		Setup:       &chatchannel.SetupDefinition{Kind: chatchannel.SetupKindQRCode},
		ConfigSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"bot_token": map[string]any{
					"type":        "string",
					"title":       "Bot token",
					"description": "Tencent iLink bot token.",
					"format":      "password",
					"writeOnly":   true,
				},
				"base_url": map[string]any{
					"type":        "string",
					"title":       "iLink API URL",
					"default":     DefaultBaseURL,
					"description": "Override only for a compatible Tencent iLink gateway.",
				},
				"cursor_file": map[string]any{
					"type":        "string",
					"title":       "Cursor file",
					"description": "Optional path for the get_updates_buf cursor. By default Server stores it under <data>/weixin/<trigger-id>.sync.",
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
			"required":             []any{"bot_token"},
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
	triggerID := strings.TrimSpace(instance.TriggerID)
	if strings.TrimSpace(config.CursorFile) == "" {
		config.CursorFile = cursorFile(factory.CursorDirectory, triggerID)
	}
	logger := factory.loggerFor(triggerID)
	client := factory.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: pollTimeout}
	}
	return &Channel{
		triggerID: triggerID,
		config:    config,
		handler:   instance.Handler,
		logger:    logger,
		client:    client,
		wechatUIN: randomWeChatUIN(),
	}, nil
}

func (factory Factory) loggerFor(triggerID string) *slog.Logger {
	logger := factory.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return logger.With(
		"component", "chat_channel",
		"channel", ChannelID,
	)
}

func ParseConfig(raw map[string]any) (Config, error) {
	allowed := map[string]struct{}{
		"bot_token": {}, "base_url": {}, "cursor_file": {}, "unsupported_message": {}, "failure_message": {},
	}
	for key := range raw {
		if _, ok := allowed[key]; !ok {
			return Config{}, fmt.Errorf("unsupported config field %q", key)
		}
	}
	config := Config{
		BotToken:           configString(raw, "bot_token"),
		BaseURL:            configString(raw, "base_url"),
		CursorFile:         configString(raw, "cursor_file"),
		UnsupportedMessage: configString(raw, "unsupported_message"),
		FailureMessage:     configString(raw, "failure_message"),
	}
	if config.BaseURL == "" {
		config.BaseURL = DefaultBaseURL
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
	if strings.TrimSpace(config.BotToken) == "" {
		return errors.New("bot_token is required")
	}
	parsed, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil {
		return fmt.Errorf("parse base_url: %w", err)
	}
	if parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("base_url must be an absolute http:// or https:// URL: %q", config.BaseURL)
	}
	return nil
}

func configString(raw map[string]any, key string) string {
	value, _ := raw[key].(string)
	return strings.TrimSpace(value)
}

type Channel struct {
	triggerID       string
	config          Config
	handler         chatchannel.Handler
	logger          *slog.Logger
	client          *http.Client
	wechatUIN       string
	typingMu        sync.Mutex
	typingTickets   map[string]cachedTypingTicket
	typingKeepalive time.Duration
}

type cachedTypingTicket struct {
	value     string
	expiresAt time.Time
}

type typingSession struct {
	channel   *Channel
	messageID string
	recipient string
	ticket    string
	done      chan struct{}
	stopped   chan struct{}
	stopOnce  sync.Once
}

func (channel *Channel) Run(ctx context.Context) error {
	if channel == nil || channel.handler == nil {
		return errors.New("wechat channel handler is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, stopWorkers := context.WithCancel(ctx)
	workerErrors := make(chan error, 1)
	var workers sync.WaitGroup
	defer func() {
		stopWorkers()
		workers.Wait()
	}()
	cursor, err := readCursor(channel.config.CursorFile)
	if err != nil {
		channel.logger.Error("WeChat cursor read failed", "error", err)
		return fmt.Errorf("read WeChat cursor: %w", err)
	}
	channel.logger.Info("WeChat channel starting", "has_cursor", cursor != "")
	defer channel.logger.Info("WeChat channel stopped")
	for {
		select {
		case workerErr := <-workerErrors:
			return workerErr
		default:
		}
		channel.logger.Debug("WeChat channel polling", "has_cursor", cursor != "")
		response, err := channel.getUpdates(runCtx, cursor)
		if err != nil {
			if ctx.Err() != nil {
				channel.logger.Debug("WeChat poll canceled")
				return nil
			}
			select {
			case workerErr := <-workerErrors:
				return workerErr
			default:
			}
			if runCtx.Err() != nil {
				return nil
			}
			var tokenErr *tokenError
			if errors.As(err, &tokenErr) {
				channel.logger.Error("WeChat channel authentication failed", "error", err)
				return err
			}
			channel.logger.Error("WeChat poll failed", "error", err, "retry_in", reconnectDelay)
			if err := waitForRetry(ctx, reconnectDelay); err != nil {
				return nil
			}
			continue
		}

		nextCursor := cursor
		if strings.TrimSpace(response.Cursor) != "" {
			nextCursor = response.Cursor
		}
		channel.logger.Debug("WeChat poll completed",
			"message_count", len(response.Messages),
			"cursor_changed", nextCursor != cursor,
		)
		if len(response.Messages) > 0 {
			channel.logger.Info("WeChat message batch received", "message_count", len(response.Messages))
		}
		channel.handleMessages(runCtx, response.Messages, &workers, stopWorkers, workerErrors)
		if nextCursor != cursor {
			if err := writeCursor(channel.config.CursorFile, nextCursor); err != nil {
				channel.logger.Error("WeChat cursor persist failed", "error", err)
				return fmt.Errorf("persist WeChat cursor: %w", err)
			}
			cursor = nextCursor
			channel.logger.Debug("WeChat cursor persisted")
		}
	}
}

func (channel *Channel) getUpdates(ctx context.Context, cursor string) (getUpdatesResponse, error) {
	request := getUpdatesRequest{
		Cursor: cursor,
		BaseInfo: baseInfo{
			ChannelVersion: "2.4.6",
			BotAgent:       botAgent,
		},
	}
	var response getUpdatesResponse
	if err := channel.doJSON(ctx, "/ilink/bot/getupdates", request, &response); err != nil {
		return getUpdatesResponse{}, err
	}
	if err := response.businessError("getupdates"); err != nil {
		return getUpdatesResponse{}, err
	}
	return response, nil
}

func (channel *Channel) handleMessages(ctx context.Context, messages []inboundMessage, workers *sync.WaitGroup, stopWorkers context.CancelFunc, workerErrors chan<- error) {
	for _, message := range messages {
		message := message
		workers.Add(1)
		go func() {
			defer workers.Done()
			err := channel.handleMessage(ctx, message)
			if err == nil || ctx.Err() != nil {
				return
			}
			var tokenErr *tokenError
			if errors.As(err, &tokenErr) {
				channel.logger.Error("WeChat message handling stopped by authentication failure", "error", err)
				select {
				case workerErrors <- err:
				default:
				}
				stopWorkers()
				return
			}
			channel.logger.Error("WeChat message handling failed", "message_id", message.messageIDString(), "error", err)
		}()
	}
}

func (channel *Channel) handleMessage(ctx context.Context, incoming inboundMessage) error {
	messageID := incoming.messageIDString()
	channel.logger.Debug("WeChat message received",
		"message_id", messageID,
		"message_type", incoming.MessageType,
		"message_state", incoming.MessageState,
		"is_group", strings.TrimSpace(incoming.GroupID) != "",
	)
	recipient := strings.TrimSpace(incoming.FromUserID)
	if recipient == "" {
		channel.logger.Error("WeChat message rejected", "message_id", messageID, "reason", "missing_sender")
		return errors.New("wechat message is missing from_user_id")
	}
	if strings.TrimSpace(incoming.GroupID) != "" {
		err := channel.sendText(ctx, recipient, incoming.ContextToken, channel.config.UnsupportedMessage)
		if err == nil {
			channel.logger.Info("WeChat unsupported group message handled", "message_id", messageID)
		}
		return err
	}
	content := incoming.textContent()
	if incoming.MessageType != 0 && incoming.MessageType != 1 {
		err := channel.sendText(ctx, recipient, incoming.ContextToken, channel.config.UnsupportedMessage)
		if err == nil {
			channel.logger.Info("WeChat unsupported message handled", "message_id", messageID, "message_type", incoming.MessageType)
		}
		return err
	}
	if strings.TrimSpace(content) == "" {
		err := channel.sendText(ctx, recipient, incoming.ContextToken, channel.config.UnsupportedMessage)
		if err == nil {
			channel.logger.Info("WeChat empty text message handled", "message_id", messageID)
		}
		return err
	}
	if strings.TrimSpace(incoming.ContextToken) == "" {
		channel.logger.Error("WeChat message rejected", "message_id", messageID, "reason", "missing_context_token")
		return errors.New("wechat message is missing context_token")
	}

	sink := newReplySink(channel, recipient, incoming.ContextToken)
	invocationCtx, cancel := context.WithTimeout(ctx, chatInvocationTimeout)
	defer cancel()
	typingCtx, cancelTyping := context.WithTimeout(invocationCtx, typingRequestTimeout)
	typing, typingErr := channel.startTyping(typingCtx, invocationCtx, messageID, recipient, incoming.ContextToken)
	cancelTyping()
	if typingErr != nil {
		channel.logger.Error("WeChat typing start failed", "message_id", messageID, "error", typingErr)
	} else {
		channel.logger.Debug("WeChat typing started", "message_id", messageID)
	}
	if typing != nil {
		defer typing.stop(ctx)
	}
	message := chatcap.InboundMessage{
		ID:             messageID,
		UserID:         recipient,
		ConversationID: firstNonEmpty(recipient, incoming.SessionID, messageID),
		Content:        content,
		Metadata: map[string]any{
			"channel":       ChannelID,
			"session_id":    incoming.SessionID,
			"seq":           incoming.Seq,
			"sender_id":     recipient,
			"message_type":  incoming.MessageType,
			"message_state": incoming.MessageState,
		},
	}
	handleErr := channel.handler.Handle(invocationCtx, message, sink)
	if typing != nil {
		typing.stop(ctx)
	}
	if handleErr != nil {
		channel.logger.Error("WeChat message trigger failed", "message_id", message.ID, "error", handleErr)
		return sink.fail(context.WithoutCancel(ctx))
	}
	channel.logger.Info("WeChat message handled", "message_id", message.ID)
	return nil
}

func (channel *Channel) startTyping(requestCtx, keepaliveCtx context.Context, messageID, recipient, contextToken string) (*typingSession, error) {
	ticket, err := channel.typingTicket(requestCtx, recipient, contextToken)
	if err != nil {
		return nil, err
	}
	if err := channel.sendTyping(requestCtx, recipient, ticket, typingStatusActive); err != nil {
		channel.invalidateTypingTicket(recipient, ticket)
		return nil, err
	}
	session := &typingSession{
		channel:   channel,
		messageID: messageID,
		recipient: recipient,
		ticket:    ticket,
		done:      make(chan struct{}),
		stopped:   make(chan struct{}),
	}
	go session.keepalive(keepaliveCtx)
	return session, nil
}

func (channel *Channel) typingTicket(ctx context.Context, recipient, contextToken string) (string, error) {
	now := time.Now()
	channel.typingMu.Lock()
	cached := channel.typingTickets[recipient]
	channel.typingMu.Unlock()
	if cached.value != "" && now.Before(cached.expiresAt) {
		return cached.value, nil
	}

	request := getTypingConfigRequest{
		WeChatUserID: recipient,
		ContextToken: contextToken,
		BaseInfo:     baseInfo{ChannelVersion: "2.4.6", BotAgent: botAgent},
	}
	var response getTypingConfigResponse
	if err := channel.doJSON(ctx, "/ilink/bot/getconfig", request, &response); err != nil {
		return "", err
	}
	if err := response.businessError("getconfig"); err != nil {
		return "", err
	}
	ticket := strings.TrimSpace(response.TypingTicket)
	if ticket == "" {
		return "", errors.New("iLink getconfig returned an empty typing_ticket")
	}
	channel.typingMu.Lock()
	if channel.typingTickets == nil {
		channel.typingTickets = make(map[string]cachedTypingTicket)
	}
	channel.typingTickets[recipient] = cachedTypingTicket{value: ticket, expiresAt: now.Add(typingTicketTTL)}
	channel.typingMu.Unlock()
	return ticket, nil
}

func (channel *Channel) invalidateTypingTicket(recipient, ticket string) {
	channel.typingMu.Lock()
	defer channel.typingMu.Unlock()
	if cached := channel.typingTickets[recipient]; cached.value == ticket {
		delete(channel.typingTickets, recipient)
	}
}

func (session *typingSession) keepalive(ctx context.Context) {
	defer close(session.stopped)
	interval := session.channel.typingKeepalive
	if interval <= 0 {
		interval = typingKeepaliveInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-session.done:
			return
		case <-ticker.C:
			requestCtx, cancel := context.WithTimeout(ctx, typingRequestTimeout)
			err := session.channel.sendTyping(requestCtx, session.recipient, session.ticket, typingStatusActive)
			cancel()
			if err != nil {
				session.channel.logger.Error("WeChat typing keepalive failed", "message_id", session.messageID, "error", err)
			}
		}
	}
}

func (session *typingSession) stop(ctx context.Context) {
	if session == nil {
		return
	}
	session.stopOnce.Do(func() {
		close(session.done)
		<-session.stopped
		session.channel.stopTyping(ctx, session.messageID, session.recipient, session.ticket)
	})
}

func (channel *Channel) stopTyping(ctx context.Context, messageID, recipient, ticket string) {
	if strings.TrimSpace(ticket) == "" {
		return
	}
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), typingRequestTimeout)
	defer cancel()
	if err := channel.sendTyping(stopCtx, recipient, ticket, typingStatusCancel); err != nil {
		channel.logger.Error("WeChat typing stop failed", "message_id", messageID, "error", err)
		return
	}
	channel.logger.Debug("WeChat typing stopped", "message_id", messageID)
}

func (channel *Channel) sendTyping(ctx context.Context, recipient, ticket string, status int) error {
	request := sendTypingRequest{
		WeChatUserID: recipient,
		TypingTicket: ticket,
		Status:       status,
		BaseInfo:     baseInfo{ChannelVersion: "2.4.6", BotAgent: botAgent},
	}
	var response apiResponse
	if err := channel.doJSON(ctx, "/ilink/bot/sendtyping", request, &response); err != nil {
		return err
	}
	return response.businessError("sendtyping")
}

func (channel *Channel) sendText(ctx context.Context, recipient, contextToken, content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	if strings.TrimSpace(contextToken) == "" {
		return errors.New("wechat reply context_token is required")
	}
	request := sendMessageRequest{
		Message: sendMessage{
			ToUserID:     recipient,
			ClientID:     uuid.NewString(),
			MessageType:  2,
			MessageState: 2,
			ContextToken: contextToken,
			ItemList:     []messageItem{{Type: 1, TextItem: &textItem{Text: content}}},
		},
		BaseInfo: baseInfo{ChannelVersion: "2.4.6", BotAgent: botAgent},
	}
	delay := sendRetryDelay
	var lastErr error
	for attempt := 0; attempt < sendRetryAttempts; attempt++ {
		channel.logger.Debug("WeChat reply sending", "attempt", attempt+1, "content_bytes", len([]byte(content)))
		var response apiResponse
		lastErr = channel.doJSON(ctx, "/ilink/bot/sendmessage", request, &response)
		if lastErr == nil {
			lastErr = response.businessError("sendmessage")
		}
		if lastErr == nil {
			channel.logger.Info("WeChat reply sent", "attempt", attempt+1, "content_bytes", len([]byte(content)))
			return nil
		}
		var tokenErr *tokenError
		willRetry := !errors.As(lastErr, &tokenErr) && ctx.Err() == nil && attempt < sendRetryAttempts-1
		channel.logger.Error("WeChat reply send failed", "attempt", attempt+1, "will_retry", willRetry, "error", lastErr)
		if !willRetry {
			return lastErr
		}
		if err := waitForRetry(ctx, delay); err != nil {
			return err
		}
		delay *= 2
	}
	return lastErr
}

func (channel *Channel) doJSON(ctx context.Context, path string, body any, result any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	base := strings.TrimRight(channel.config.BaseURL, "/")
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode %s request: %w", path, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create %s request: %w", path, err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("AuthorizationType", "ilink_bot_token")
	request.Header.Set("Authorization", "Bearer "+channel.config.BotToken)
	request.Header.Set("X-WECHAT-UIN", channel.wechatUIN)
	request.Header.Set("iLink-App-Id", apiID)
	request.Header.Set("iLink-App-ClientVersion", apiClientVersion)
	response, err := channel.client.Do(request)
	if err != nil {
		return fmt.Errorf("%s request failed: %w", path, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			return &tokenError{Code: response.StatusCode}
		}
		return fmt.Errorf("%s returned HTTP %d", path, response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxMessageSize))
	if err := decoder.Decode(result); err != nil {
		return fmt.Errorf("decode %s response: %w", path, err)
	}
	return nil
}

type replySink struct {
	channel      *Channel
	recipient    string
	contextToken string
	mu           sync.Mutex
	lastUpdate   string
	finished     bool
}

func newReplySink(channel *Channel, recipient, contextToken string) *replySink {
	return &replySink{channel: channel, recipient: recipient, contextToken: contextToken}
}

func (sink *replySink) Emit(ctx context.Context, reply chatcap.Reply) error {
	if sink == nil || sink.channel == nil {
		return chatcap.ErrReplySinkUnavailable
	}
	switch reply.Kind {
	case chatcap.ReplyUpdate:
		if strings.TrimSpace(reply.Content) == "" {
			return nil
		}
		sink.mu.Lock()
		if !sink.finished {
			sink.lastUpdate = reply.Content
		}
		sink.mu.Unlock()
		return nil
	case chatcap.ReplyMessage:
		if strings.TrimSpace(reply.Content) == "" {
			return nil
		}
		sink.mu.Lock()
		finished := sink.finished
		sink.mu.Unlock()
		if finished {
			return nil
		}
		return sink.channel.sendText(ctx, sink.recipient, sink.contextToken, reply.Content)
	case chatcap.ReplyFinish:
		return sink.finish(ctx, reply.Content, reply.Error != "")
	default:
		return fmt.Errorf("unsupported WeChat chat reply kind %q", reply.Kind)
	}
}

func (sink *replySink) fail(ctx context.Context) error {
	return sink.finish(ctx, sink.channel.config.FailureMessage, true)
}

func (sink *replySink) finish(ctx context.Context, content string, failure bool) error {
	sink.mu.Lock()
	if sink.finished {
		sink.mu.Unlock()
		return nil
	}
	sink.finished = true
	if failure {
		content = sink.channel.config.FailureMessage
	} else if strings.TrimSpace(content) == "" {
		content = sink.lastUpdate
	}
	sink.mu.Unlock()
	return sink.channel.sendText(ctx, sink.recipient, sink.contextToken, content)
}

type baseInfo struct {
	ChannelVersion string `json:"channel_version"`
	BotAgent       string `json:"bot_agent"`
}

type getUpdatesRequest struct {
	Cursor   string   `json:"get_updates_buf"`
	BaseInfo baseInfo `json:"base_info"`
}

type getUpdatesResponse struct {
	apiResponse
	Messages []inboundMessage `json:"msgs"`
	Cursor   string           `json:"get_updates_buf"`
}

type getTypingConfigRequest struct {
	WeChatUserID string   `json:"ilink_user_id"`
	ContextToken string   `json:"context_token"`
	BaseInfo     baseInfo `json:"base_info"`
}

type getTypingConfigResponse struct {
	apiResponse
	TypingTicket string `json:"typing_ticket"`
}

type sendTypingRequest struct {
	WeChatUserID string   `json:"ilink_user_id"`
	TypingTicket string   `json:"typing_ticket"`
	Status       int      `json:"status"`
	BaseInfo     baseInfo `json:"base_info"`
}

type apiResponse struct {
	Ret     *int   `json:"ret,omitempty"`
	ErrCode *int   `json:"errcode,omitempty"`
	ErrMsg  string `json:"errmsg,omitempty"`
}

func (response apiResponse) businessError(operation string) error {
	if response.Ret != nil && *response.Ret != 0 {
		if *response.Ret == -14 {
			return &tokenError{Code: *response.Ret}
		}
		return fmt.Errorf("iLink %s rejected: ret=%d errmsg=%s", operation, *response.Ret, response.ErrMsg)
	}
	if response.ErrCode != nil && *response.ErrCode != 0 {
		if *response.ErrCode == -14 {
			return &tokenError{Code: *response.ErrCode}
		}
		return fmt.Errorf("iLink %s rejected: errcode=%d errmsg=%s", operation, *response.ErrCode, response.ErrMsg)
	}
	return nil
}

type tokenError struct {
	Code int
}

func (err *tokenError) Error() string {
	if err == nil {
		return "iLink authentication failed"
	}
	if err.Code == http.StatusUnauthorized || err.Code == http.StatusForbidden {
		return fmt.Sprintf("iLink authentication failed: HTTP %d", err.Code)
	}
	return fmt.Sprintf("iLink token rejected: code=%d", err.Code)
}

type inboundMessage struct {
	Seq          int64           `json:"seq,omitempty"`
	MessageID    json.RawMessage `json:"message_id,omitempty"`
	FromUserID   string          `json:"from_user_id,omitempty"`
	ToUserID     string          `json:"to_user_id,omitempty"`
	SessionID    string          `json:"session_id,omitempty"`
	GroupID      string          `json:"group_id,omitempty"`
	MessageType  int             `json:"message_type,omitempty"`
	MessageState int             `json:"message_state,omitempty"`
	ContextToken string          `json:"context_token,omitempty"`
	ItemList     []messageItem   `json:"item_list,omitempty"`
}

type messageItem struct {
	Type     int       `json:"type,omitempty"`
	TextItem *textItem `json:"text_item,omitempty"`
}

type textItem struct {
	Text string `json:"text,omitempty"`
}

type sendMessageRequest struct {
	Message  sendMessage `json:"msg"`
	BaseInfo baseInfo    `json:"base_info"`
}

type sendMessage struct {
	FromUserID   string        `json:"from_user_id"`
	ToUserID     string        `json:"to_user_id"`
	ClientID     string        `json:"client_id"`
	MessageType  int           `json:"message_type"`
	MessageState int           `json:"message_state"`
	ItemList     []messageItem `json:"item_list"`
	ContextToken string        `json:"context_token"`
	RunID        string        `json:"run_id,omitempty"`
}

func (message inboundMessage) textContent() string {
	for _, item := range message.ItemList {
		if item.Type == 1 && item.TextItem != nil {
			return strings.TrimSpace(item.TextItem.Text)
		}
	}
	return ""
}

func (message inboundMessage) messageIDString() string {
	if len(message.MessageID) == 0 || string(message.MessageID) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(message.MessageID, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var number json.Number
	if err := json.Unmarshal(message.MessageID, &number); err == nil {
		return number.String()
	}
	return strings.Trim(strings.TrimSpace(string(message.MessageID)), `"`)
}

func randomWeChatUIN() string {
	var randomBytes [4]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		return base64.StdEncoding.EncodeToString([]byte("0"))
	}
	value := binary.BigEndian.Uint32(randomBytes[:])
	return base64.StdEncoding.EncodeToString([]byte(strconv.FormatUint(uint64(value), 10)))
}

func cursorFile(directory, triggerID string) string {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		directory = DefaultCursorDirectory
	}
	triggerID = strings.TrimSpace(triggerID)
	if triggerID == "" {
		triggerID = "default"
	}
	triggerID = strings.NewReplacer(`\`, "_", "/", "_", ":", "_").Replace(triggerID)
	return filepath.Join(directory, triggerID+".sync")
}

func readCursor(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\r\n"), nil
}

func writeCursor(path, cursor string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("cursor file is empty")
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(cursor+"\n"), 0o600)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
