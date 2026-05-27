package neo

import (
	"encoding/json"
	"testing"

	fruntime "weaveflow/runtime"
)

func TestTranslateEventStreamsReasoningChunk(t *testing.T) {
	t.Parallel()

	event := fruntime.Event{
		Type:    fruntime.EventLLMReasoningChunk,
		NodeID:  "LLM_test",
		Payload: rawJSON(map[string]string{"text": "thinking..."})}

	got := TranslateEvent(event)
	if got == nil {
		t.Fatal("TranslateEvent() = nil, want chat event")
	}
	if got.Type != ChatEventTypeThinking {
		t.Fatalf("TranslateEvent().Type = %q, want %q", got.Type, ChatEventTypeThinking)
	}
	if got.Content != "thinking..." {
		t.Fatalf("TranslateEvent().Content = %q, want %q", got.Content, "thinking...")
	}
}

func TestTranslateEventStreamsContentSummary(t *testing.T) {
	t.Parallel()

	event := fruntime.Event{
		Type:    fruntime.EventLLMContent,
		NodeID:  "Finalizer_test",
		Payload: rawJSON(map[string]string{"text": "done"}),
	}

	got := TranslateEvent(event)
	if got == nil {
		t.Fatal("TranslateEvent() = nil, want chat event")
	}
	if got.Type != ChatEventTypeGenerating {
		t.Fatalf("TranslateEvent().Type = %q, want %q", got.Type, ChatEventTypeGenerating)
	}
	if got.Content != "done" {
		t.Fatalf("TranslateEvent().Content = %q, want %q", got.Content, "done")
	}
}

func TestTranslateEventKeepsBatchedToolPayload(t *testing.T) {
	t.Parallel()

	event := fruntime.Event{
		Type: fruntime.EventToolCalled,
		Payload: rawJSON(map[string]any{
			"count":    2,
			"parallel": true,
			"tools": []map[string]string{
				{"tool_call_id": "call_1", "name": "alpha", "arguments": `{"input":"one"}`},
				{"tool_call_id": "call_2", "name": "beta", "arguments": `{"input":"two"}`},
			},
		}),
	}

	got := TranslateEvent(event)
	if got == nil {
		t.Fatal("TranslateEvent() = nil, want chat event")
	}
	if got.Type != ChatEventTypeToolCall {
		t.Fatalf("TranslateEvent().Type = %q, want %q", got.Type, ChatEventTypeToolCall)
	}

	var data struct {
		Tools []struct {
			ToolCallID string `json:"tool_call_id"`
			Name       string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(got.Data, &data); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if len(data.Tools) != 2 || data.Tools[0].ToolCallID != "call_1" || data.Tools[1].Name != "beta" {
		t.Fatalf("batched tool data = %#v", data.Tools)
	}
}

func TestTranslateEventReportsExploreStep(t *testing.T) {
	t.Parallel()

	got := TranslateEvent(fruntime.Event{
		Type:   fruntime.EventNodeStarted,
		NodeID: "Explore_test",
	})
	if got == nil {
		t.Fatal("TranslateEvent() = nil, want chat event")
	}
	if got.Type != ChatEventTypeStep {
		t.Fatalf("TranslateEvent().Type = %q, want %q", got.Type, ChatEventTypeStep)
	}
	if got.Content != "正在浏览文件..." {
		t.Fatalf("TranslateEvent().Content = %q, want 正在浏览文件...", got.Content)
	}
}

func TestAttachEventIdentityIncludesStepAndNode(t *testing.T) {
	t.Parallel()

	event := fruntime.Event{
		Type:   fruntime.EventLLMReasoningChunk,
		NodeID: "Planner_test",
		StepID: "step_123",
		Payload: rawJSON(map[string]string{
			"text": "plan first",
		}),
	}

	got := attachEventIdentity(event, TranslateEvent(event))
	if got == nil {
		t.Fatal("attachEventIdentity() = nil, want chat event")
	}

	var data map[string]string
	if err := json.Unmarshal(got.Data, &data); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if data["node_id"] != "Planner_test" {
		t.Fatalf("data[node_id] = %q, want %q", data["node_id"], "Planner_test")
	}
	if data["step_id"] != "step_123" {
		t.Fatalf("data[step_id] = %q, want %q", data["step_id"], "step_123")
	}
}

func TestAttachEventIdentityIncludesNodeForToolEvents(t *testing.T) {
	t.Parallel()

	event := fruntime.Event{
		Type:   fruntime.EventToolReturned,
		NodeID: "Explore_test",
		StepID: "step_456",
		Payload: rawJSON(map[string]string{
			"tool_call_id": "call_1",
			"name":         "read",
			"content":      "ok",
			"arguments":    `{"path":"nodes/explore.go"}`,
		}),
	}

	got := attachEventIdentity(event, TranslateEvent(event))
	if got == nil {
		t.Fatal("attachEventIdentity() = nil, want chat event")
	}

	var data map[string]string
	if err := json.Unmarshal(got.Data, &data); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if data["node_id"] != "Explore_test" {
		t.Fatalf("data[node_id] = %q, want Explore_test", data["node_id"])
	}
	if data["step_id"] != "step_456" {
		t.Fatalf("data[step_id] = %q, want step_456", data["step_id"])
	}
	if data["arguments"] != `{"path":"nodes/explore.go"}` {
		t.Fatalf("data[arguments] = %q", data["arguments"])
	}
}

func TestSyncReasoningSummaryReturnsSuffixAfterPartialStream(t *testing.T) {
	t.Parallel()

	streamed := make(map[string]string)
	chunk := fruntime.Event{
		Type:    fruntime.EventLLMReasoningChunk,
		NodeID:  "Planner_test",
		StepID:  "step_123",
		Payload: rawJSON(map[string]string{"text": "plan "}),
	}
	rememberStreamedReasoningText(chunk, attachEventIdentity(chunk, TranslateEvent(chunk)), streamed)

	summary := fruntime.Event{
		Type:    fruntime.EventLLMReasoning,
		NodeID:  "Planner_test",
		StepID:  "step_123",
		Payload: rawJSON(map[string]string{"text": "plan first"}),
	}

	got := syncReasoningSummary(summary, attachEventIdentity(summary, TranslateEvent(summary)), streamed)
	if got == nil {
		t.Fatal("syncReasoningSummary() = nil, want suffix event")
	}
	if got.Content != "first" {
		t.Fatalf("syncReasoningSummary().Content = %q, want %q", got.Content, "first")
	}
}

func TestSyncReasoningSummarySkipsDuplicateFullText(t *testing.T) {
	t.Parallel()

	streamed := make(map[string]string)
	chunk := fruntime.Event{
		Type:    fruntime.EventLLMReasoningChunk,
		NodeID:  "Planner_test",
		StepID:  "step_123",
		Payload: rawJSON(map[string]string{"text": "plan first"}),
	}
	rememberStreamedReasoningText(chunk, attachEventIdentity(chunk, TranslateEvent(chunk)), streamed)

	summary := fruntime.Event{
		Type:    fruntime.EventLLMReasoning,
		NodeID:  "Planner_test",
		StepID:  "step_123",
		Payload: rawJSON(map[string]string{"text": "plan first"}),
	}

	if got := syncReasoningSummary(summary, attachEventIdentity(summary, TranslateEvent(summary)), streamed); got != nil {
		t.Fatalf("syncReasoningSummary() = %#v, want nil", got)
	}
}

func TestSyncReasoningSummaryFallsBackWithoutStreamedChunk(t *testing.T) {
	t.Parallel()

	streamed := make(map[string]string)
	summary := fruntime.Event{
		Type:    fruntime.EventLLMReasoning,
		NodeID:  "Verifier_test",
		StepID:  "step_456",
		Payload: rawJSON(map[string]string{"text": "looks good"}),
	}

	got := syncReasoningSummary(summary, attachEventIdentity(summary, TranslateEvent(summary)), streamed)
	if got == nil {
		t.Fatal("syncReasoningSummary() = nil, want chat event")
	}
	if got.Type != ChatEventTypeThinking {
		t.Fatalf("syncReasoningSummary().Type = %q, want %q", got.Type, ChatEventTypeThinking)
	}
	if got.Content != "looks good" {
		t.Fatalf("syncReasoningSummary().Content = %q, want %q", got.Content, "looks good")
	}
}

func TestSyncContentSummarySkipsDuplicateFullText(t *testing.T) {
	t.Parallel()

	streamed := make(map[string]string)
	chunk := fruntime.Event{
		Type:    fruntime.EventLLMContentChunk,
		NodeID:  "Finalizer_test",
		StepID:  "step_789",
		Payload: rawJSON(map[string]string{"text": "answer"}),
	}
	rememberStreamedContentText(chunk, attachEventIdentity(chunk, TranslateEvent(chunk)), streamed)

	summary := fruntime.Event{
		Type:    fruntime.EventLLMContent,
		NodeID:  "Finalizer_test",
		StepID:  "step_789",
		Payload: rawJSON(map[string]string{"text": "answer"}),
	}

	if got := syncContentSummary(summary, attachEventIdentity(summary, TranslateEvent(summary)), streamed); got != nil {
		t.Fatalf("syncContentSummary() = %#v, want nil", got)
	}
}
