package server

import (
	"log/slog"
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
	subscription := s.events.Subscribe(filter, cursor)
	defer subscription.Unsubscribe()

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	if subscription.Replay.Gap {
		gap := runtimeEventStreamGap{
			Type:              "stream.gap",
			GraphID:           graphID,
			RequestedCursor:   subscription.Replay.RequestedCursor,
			OldestEventID:     subscription.Replay.OldestEventID,
			ResumeCursor:      subscription.Replay.ResumeCursor,
			Reason:            subscription.Replay.Reason,
			RecoverableEvents: "persistent_only",
		}
		if err := writeSSEJSON(c.Writer, "", "", gap); err != nil {
			logRuntimeEventStreamClose(graphID, filter, subscription.SubscriberID, sseCloseReason(err), "", "", err)
			return
		}
	}
	if err := writeSSEHeartbeat(c.Writer); err != nil {
		logRuntimeEventStreamClose(graphID, filter, subscription.SubscriberID, sseCloseReason(err), "", "", err)
		return
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.Request.Context().Done():
			logRuntimeEventStreamClose(graphID, filter, subscription.SubscriberID, "request_context_canceled", "", "", c.Request.Context().Err())
			return
		case closeState, ok := <-subscription.Closed:
			if ok && closeState.Reason != "unsubscribed" {
				logRuntimeEventStreamClose(graphID, filter, subscription.SubscriberID, closeState.Reason, "", "", nil)
			}
			return
		case event, ok := <-subscription.Events:
			if !ok {
				return
			}
			if err := writeRuntimeEventSSE(c.Writer, event); err != nil {
				logRuntimeEventStreamClose(graphID, filter, subscription.SubscriberID, sseCloseReason(err), event.ID, string(event.Type), err)
				return
			}
		case <-ticker.C:
			if err := writeSSEHeartbeat(c.Writer); err != nil {
				logRuntimeEventStreamClose(graphID, filter, subscription.SubscriberID, sseCloseReason(err), "", "", err)
				return
			}
		}
	}
}

type runtimeEventStreamGap struct {
	Type              string `json:"type"`
	GraphID           string `json:"graph_id"`
	RequestedCursor   string `json:"requested_cursor"`
	OldestEventID     string `json:"oldest_event_id,omitempty"`
	ResumeCursor      string `json:"resume_cursor,omitempty"`
	Reason            string `json:"reason"`
	RecoverableEvents string `json:"recoverable_events"`
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

func writeRuntimeEventSSE(writer http.ResponseWriter, event runtime.Event) error {
	return writeSSEJSON(writer, "", event.ID, event)
}

func writeSSEHeartbeat(writer http.ResponseWriter) error {
	return writeSSEComment(writer, "heartbeat")
}

func sanitizeSSEField(value string) string {
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, "\n", "")
	return value
}

func logRuntimeEventStreamClose(graphID string, filter eventFilter, subscriberID int, reason, eventID, eventType string, err error) {
	slog.Debug("runtime event stream closed")
}
