package utilities

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	fruntime "weaveflow/runtime"
)

func TestPrettyEventLoggingPrintsBatchedToolCalledPayload(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	logger := NewPrettyEventLogging(&out, WithColors(false))
	event := prettyTestEvent(t, fruntime.EventToolCalled, map[string]any{
		"tools": []map[string]any{
			{"name": "alpha", "arguments": `{"input":"one"}`},
			{"name": "beta", "arguments": `{"input":"two"}`},
		},
		"count":    2,
		"parallel": true,
	})

	if err := logger.Publish(t.Context(), event); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	got := out.String()
	if !strings.Contains(got, `alpha({"input":"one"})`) {
		t.Fatalf("output missing alpha tool call: %q", got)
	}
	if !strings.Contains(got, `beta({"input":"two"})`) {
		t.Fatalf("output missing beta tool call: %q", got)
	}
}

func TestFormatToolCallPayloadPutsWriteFilePathFirst(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		arguments any
	}{
		{
			name:      "JSON string",
			arguments: `{"content":"hello","file_path":"notes.txt"}`,
		},
		{
			name: "map",
			arguments: map[string]any{
				"content":   "hello",
				"file_path": "notes.txt",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := formatToolCallPayload(map[string]any{
				"name":      "write",
				"arguments": tt.arguments,
			})
			want := `write({"file_path":"notes.txt","content":"hello"})`
			if got != want {
				t.Fatalf("formatToolCallPayload() = %q, want %q", got, want)
			}
		})
	}
}

func TestPrettyEventLoggingPrintsToolReturnedContent(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	logger := NewPrettyEventLogging(&out, WithColors(false))
	event := prettyTestEvent(t, fruntime.EventToolReturned, map[string]any{
		"tool_call_id": "call_1",
		"name":         "calculator",
		"content":      "42",
	})

	if err := logger.Publish(t.Context(), event); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	got := out.String()
	if !strings.Contains(got, `calculator → 42`) {
		t.Fatalf("output missing tool return content: %q", got)
	}
}

func prettyTestEvent(t *testing.T, eventType fruntime.EventType, payload any) fruntime.Event {
	t.Helper()

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return fruntime.Event{
		Type:      eventType,
		Timestamp: time.Date(2026, 5, 27, 10, 0, 0, 0, time.UTC),
		Payload:   data,
	}
}
