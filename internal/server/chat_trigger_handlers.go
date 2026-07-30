package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	chatcap "github.com/dengzii/weaveflow/capability/chat"
	"github.com/dengzii/weaveflow/internal/trigger"
	"github.com/gin-gonic/gin"
)

const maxChatTriggerBodyBytes int64 = 1 << 20

func (s *Server) handleChatTrigger(c *gin.Context) {
	service := s.TriggerService()
	if service == nil {
		writeError(c, http.StatusServiceUnavailable, errRunnerNotConfigured)
		return
	}
	triggerID := strings.TrimSpace(c.Param("trigger_id"))
	item, err := service.Get(c.Request.Context(), triggerID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
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

func (s *Server) handleStreamingChatTrigger(c *gin.Context, ctx context.Context, service *trigger.Service, triggerID string, message chatcap.Message) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(c.Writer, ": ready\n\n")
	c.Writer.Flush()

	sink := &sseChatReplySink{writer: c.Writer}
	result, err := service.InvokeChat(ctx, triggerID, message, sink)
	if err != nil {
		writeChatSSEEvent(c.Writer, "error", map[string]any{"error": err.Error(), "result": result})
		return
	}
	writeChatSSEEvent(c.Writer, "result", result)
}

func decodeChatMessage(c *gin.Context) (chatcap.Message, error) {
	body, err := readRequestBody(c.Request.Body, maxChatTriggerBodyBytes)
	if err != nil {
		return chatcap.Message{}, err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return chatcap.Message{}, fmt.Errorf("chat message is required")
	}
	var message chatcap.Message
	if err := decodeStrictJSON(body, &message); err != nil {
		return chatcap.Message{}, err
	}
	message = message.Normalize()
	if err := message.Validate(); err != nil {
		return chatcap.Message{}, err
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
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", sanitizeSSEField(event), data); err != nil {
		return err
	}
	if flusher, ok := writer.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}
