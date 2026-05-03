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
