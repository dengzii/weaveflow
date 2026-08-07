package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dengzii/weaveflow/runtime"

	"github.com/gin-gonic/gin"
)

func (s *Server) handleRuntimeEventStream(c *gin.Context) {
	if s == nil || s.events == nil {
		writeError(c, http.StatusServiceUnavailable, errEventStreamNotConfigured)
		return
	}
	filter, err := eventFilterFromQuery(c)
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	graphID, ok := requireGraphIDPathParam(c)
	if !ok {
		return
	}
	filter.GraphID = graphID
	cursor, err := eventStreamCursor(c)
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	events, unsubscribe := s.events.Subscribe(filter, cursor)
	defer unsubscribe()

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	writeSSEHeartbeat(c)

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.Request.Context().Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			writeRuntimeEventSSE(c, event)
		case <-ticker.C:
			writeSSEHeartbeat(c)
		}
	}
}

func eventFilterFromQuery(c *gin.Context) (eventFilter, error) {
	graphSessionID, err := optionalStringQuery(c, "session_id")
	if err != nil {
		return eventFilter{}, err
	}
	runID, err := optionalStringQuery(c, "run_id")
	if err != nil {
		return eventFilter{}, err
	}
	nodeID, err := optionalStringQuery(c, "node_id")
	if err != nil {
		return eventFilter{}, err
	}
	filter := eventFilter{
		GraphSessionID: graphSessionID,
		RunID:          runID,
		NodeID:         nodeID,
	}
	types, err := stringListQuery(c, "type")
	if err != nil {
		return eventFilter{}, err
	}
	for _, item := range types {
		if filter.Types == nil {
			filter.Types = map[runtime.EventType]struct{}{}
		}
		filter.Types[runtime.EventType(item)] = struct{}{}
	}
	return filter, nil
}

func eventStreamCursor(c *gin.Context) (string, error) {
	queryCursor, err := optionalStringQuery(c, "cursor")
	if err != nil {
		return "", err
	}
	headerCursor := strings.TrimSpace(c.GetHeader("Last-Event-ID"))
	if queryCursor != "" && headerCursor != "" && queryCursor != headerCursor {
		return "", invalidRequestf("cursor and Last-Event-ID must match when both are provided")
	}
	if queryCursor != "" {
		return queryCursor, nil
	}
	return headerCursor, nil
}

func writeRuntimeEventSSE(c *gin.Context, event runtime.Event) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	if event.ID != "" {
		_, _ = fmt.Fprintf(c.Writer, "id: %s\n", sanitizeSSEField(event.ID))
	}
	if event.Type != "" {
		_, _ = fmt.Fprintf(c.Writer, "event: %s\n", sanitizeSSEField(string(event.Type)))
	}
	_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", data)
	c.Writer.Flush()
}

func writeSSEHeartbeat(c *gin.Context) {
	_, _ = fmt.Fprint(c.Writer, ": heartbeat\n\n")
	c.Writer.Flush()
}

func sanitizeSSEField(value string) string {
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, "\n", "")
	return value
}
