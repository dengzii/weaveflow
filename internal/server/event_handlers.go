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

func (s *Server) handleEventStream(c *gin.Context) {
	if s == nil || s.events == nil {
		writeError(c, http.StatusServiceUnavailable, errEventStreamNotConfigured)
		return
	}
	filter := eventFilterFromQuery(c)
	events, unsubscribe := s.events.Subscribe(filter)
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

func eventFilterFromQuery(c *gin.Context) eventFilter {
	filter := eventFilter{
		RunID:  strings.TrimSpace(c.Query("run_id")),
		NodeID: strings.TrimSpace(c.Query("node_id")),
	}
	typeValues := append([]string{}, c.QueryArray("type")...)
	typeValues = append(typeValues, c.QueryArray("types")...)
	for _, value := range typeValues {
		for _, item := range strings.Split(value, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if filter.Types == nil {
				filter.Types = map[runtime.EventType]struct{}{}
			}
			filter.Types[runtime.EventType(item)] = struct{}{}
		}
	}
	return filter
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
