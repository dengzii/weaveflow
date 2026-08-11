package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	chatcap "github.com/dengzii/weaveflow/capability/chat"
	"github.com/dengzii/weaveflow/internal/trigger"
	"github.com/gin-gonic/gin"
)

const maxChatTriggerBodyBytes int64 = 1 << 20

func (s *Server) handleChatTrigger(c *gin.Context) {
	service, item, ok := s.scopedTrigger(c)
	if !ok {
		return
	}
	triggerID := item.ID
	if item.Type != trigger.TypeChat {
		writeError(c, http.StatusBadRequest, fmt.Errorf("%w: trigger %q is not a chat trigger", trigger.ErrTypeMismatch, triggerID))
		return
	}
	if !item.Enabled {
		writeError(c, http.StatusConflict, trigger.ErrDisabled)
		return
	}
	message, err := decodeChatMessage(c)
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	ctx, cancel := s.deriveRunContext(c)
	defer cancel()

	if acceptsEventStream(c.GetHeader("Accept")) {
		s.handleStreamingChatTrigger(c, ctx, service, triggerID, message)
		return
	}
	sink := &bufferedChatReplySink{}
	result, err := service.InvokeChat(ctx, triggerID, message, sink)
	response := map[string]any{"result": result, "replies": sink.replies}
	if err != nil {
		writeErrorData(c, statusForError(err), err, response)
		return
	}
	writeData(c, http.StatusOK, response)
}

func (s *Server) handleStreamingChatTrigger(c *gin.Context, ctx context.Context, service *trigger.Service, triggerID string, message chatcap.InboundMessage) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	if err := writeSSEComment(c.Writer, "ready"); err != nil {
		slog.Debug("chat event stream closed", "trigger_id", triggerID, "reason", sseCloseReason(err), "stage", "ready", "error", err)
		return
	}

	sink := &sseChatReplySink{writer: c.Writer}
	result, err := service.InvokeChat(ctx, triggerID, message, sink)
	if err != nil {
		if writeErr := writeChatSSEEvent(c.Writer, "error", map[string]any{"error": err.Error(), "result": result}); writeErr != nil {
			slog.Debug("chat event stream closed", "trigger_id", triggerID, "reason", sseCloseReason(writeErr), "stage", "error", "error", writeErr)
		}
		return
	}
	if err := writeChatSSEEvent(c.Writer, "result", result); err != nil {
		slog.Debug("chat event stream closed", "trigger_id", triggerID, "reason", sseCloseReason(err), "stage", "result", "error", err)
	}
}

func decodeChatMessage(c *gin.Context) (chatcap.InboundMessage, error) {
	body, err := readRequestBody(c.Request.Body, maxChatTriggerBodyBytes)
	if err != nil {
		return chatcap.InboundMessage{}, err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return chatcap.InboundMessage{}, fmt.Errorf("chat message is required")
	}
	var message chatcap.InboundMessage
	if err := decodeStrictJSON(body, &message); err != nil {
		return chatcap.InboundMessage{}, err
	}
	message = message.Normalize()
	if err := message.Validate(); err != nil {
		return chatcap.InboundMessage{}, err
	}
	return message, nil
}

func acceptsEventStream(value string) bool {
	for _, item := range strings.Split(value, ",") {
		mediaType := strings.TrimSpace(strings.SplitN(item, ";", 2)[0])
		if strings.EqualFold(mediaType, "text/event-stream") {
			return true
		}
	}
	return false
}

type bufferedChatReplySink struct {
	replies []chatcap.Reply
}

func (s *bufferedChatReplySink) Emit(_ context.Context, reply chatcap.Reply) error {
	s.replies = append(s.replies, reply)
	return nil
}

type sseChatReplySink struct {
	mu     sync.Mutex
	writer http.ResponseWriter
}

func (s *sseChatReplySink) Emit(_ context.Context, reply chatcap.Reply) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeChatSSEEvent(s.writer, string(reply.Kind), reply)
}

func writeChatSSEEvent(writer http.ResponseWriter, event string, value any) error {
	return writeSSEJSON(writer, event, "", value)
}
