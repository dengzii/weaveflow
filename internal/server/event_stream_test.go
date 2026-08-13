package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	chatcap "github.com/dengzii/weaveflow/capability/chat"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/gin-gonic/gin"
)

func TestRuntimeEventSSEUsesUnnamedDataFrames(t *testing.T) {
	recorder := httptest.NewRecorder()
	event := runtime.Event{
		ID:        "event-1",
		GraphID:   "graph",
		RunID:     "run",
		Type:      runtime.EventRunStarted,
		Timestamp: time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC),
	}
	if err := writeRuntimeEventSSE(recorder, event); err != nil {
		t.Fatal(err)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "id: event-1\n") || !strings.Contains(body, `"type":"run.started"`) {
		t.Fatalf("runtime event frame = %q", body)
	}
	if strings.Contains(body, "event:") {
		t.Fatalf("runtime event unexpectedly used a named SSE event: %q", body)
	}
}

func TestRuntimeEventSSEPropagatesMarshalWriteAndFlushErrors(t *testing.T) {
	marshalEvent := runtime.Event{Payload: json.RawMessage(`{`)}
	if err := writeRuntimeEventSSE(httptest.NewRecorder(), marshalEvent); err == nil {
		t.Fatal("invalid JSON payload did not return a marshal error")
	} else if reason := sseCloseReason(err); reason != "serialization_error" {
		t.Fatalf("marshal close reason = %q", reason)
	}

	writeErr := errors.New("write failed")
	if err := writeRuntimeEventSSE(&failingSSEWriter{writeErr: writeErr}, runtime.Event{ID: "event"}); !errors.Is(err, writeErr) {
		t.Fatalf("write error = %v, want %v", err, writeErr)
	} else if reason := sseCloseReason(err); reason != "transport_write_error" {
		t.Fatalf("write close reason = %q", reason)
	}

	flushErr := errors.New("flush failed")
	if err := writeSSEHeartbeat(&failingSSEWriter{flushErr: flushErr}); !errors.Is(err, flushErr) {
		t.Fatalf("flush error = %v, want %v", err, flushErr)
	} else if reason := sseCloseReason(err); reason != "transport_flush_error" {
		t.Fatalf("flush close reason = %q", reason)
	}
}

func TestChatReplySinkPropagatesStreamWriteFailure(t *testing.T) {
	writeErr := errors.New("client disconnected")
	sink := &sseChatReplySink{writer: &failingSSEWriter{writeErr: writeErr}}
	err := sink.Emit(context.Background(), chatcap.Reply{Kind: chatcap.ReplyMessage, Content: "partial"})
	if !errors.Is(err, writeErr) {
		t.Fatalf("reply sink error = %v, want %v", err, writeErr)
	}
}

func TestRuntimeEventStreamSendsGapAndReleasesSubscription(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hub := newTestEventHub(eventHubOptions{eventHistoryLimit: 1})
	publishTestEvent(t, hub, runtime.Event{ID: "event-1", GraphID: "graph", RunID: "run", Type: runtime.EventRunStarted})
	publishTestEvent(t, hub, runtime.Event{ID: "event-2", GraphID: "graph", RunID: "run", Type: runtime.EventRunFinished})
	testServer := &Server{events: hub}
	engine := gin.New()
	engine.GET("/graphs/:graph_id/events/stream", testServer.handleRuntimeEventStream)

	requestContext, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodGet, "/graphs/graph/events/stream?cursor=event-1", nil).WithContext(requestContext)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status = %d content-type = %q", response.Code, response.Header().Get("Content-Type"))
	}
	body := response.Body.String()
	if !strings.Contains(body, `"type":"stream.gap"`) || !strings.Contains(body, `"resume_cursor":"event-2"`) {
		t.Fatalf("gap stream body = %q", body)
	}
	if !strings.Contains(body, ": heartbeat\n\n") {
		t.Fatalf("stream body missing heartbeat: %q", body)
	}
	if metrics := hub.Metrics(); metrics.CurrentSubscribers != 0 {
		t.Fatalf("subscription leaked after disconnect: %#v", metrics)
	}
}

func TestRuntimeEventStreamRejectsConflictingCursorSources(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testServer := &Server{events: newTestEventHub(eventHubOptions{})}
	engine := gin.New()
	engine.GET("/graphs/:graph_id/events/stream", testServer.handleRuntimeEventStream)
	request := httptest.NewRequest(http.MethodGet, "/graphs/graph/events/stream?cursor=query", nil)
	request.Header.Set("Last-Event-ID", "header")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", response.Code, response.Body.String())
	}
}

func TestEventStreamCursorAcceptsHeaderAndQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, testCase := range []struct {
		name   string
		path   string
		header string
		want   string
	}{
		{name: "query", path: "/?cursor=query-event", want: "query-event"},
		{name: "header", path: "/", header: "header-event", want: "header-event"},
		{name: "matching", path: "/?cursor=same-event", header: "same-event", want: "same-event"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			contextRecorder := httptest.NewRecorder()
			ginContext, _ := gin.CreateTestContext(contextRecorder)
			ginContext.Request = httptest.NewRequest(http.MethodGet, testCase.path, nil)
			ginContext.Request.Header.Set("Last-Event-ID", testCase.header)
			cursor, err := eventStreamCursor(ginContext)
			if err != nil || cursor != testCase.want {
				t.Fatalf("cursor = %q error = %v, want %q", cursor, err, testCase.want)
			}
		})
	}
}

type failingSSEWriter struct {
	header   http.Header
	writeErr error
	flushErr error
}

func (w *failingSSEWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *failingSSEWriter) Write(value []byte) (int, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return len(value), nil
}

func (w *failingSSEWriter) WriteHeader(int) {}

func (w *failingSSEWriter) FlushError() error {
	return w.flushErr
}
