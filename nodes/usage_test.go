package nodes

import (
	"context"
	"testing"

	"weaveflow/runtime"
	wfstate "weaveflow/state"

	"github.com/tmc/langchaingo/llms"
)

func TestRecordChoiceUsageRecordsStateAndPublishesEvent(t *testing.T) {
	state := wfstate.State{}

	var publishedType runtime.EventType
	var publishedPayload map[string]any
	ctx := runtime.WithRunnerEventPublisher(context.Background(), func(eventType runtime.EventType, payload any) error {
		publishedType = eventType
		if typed, ok := payload.(map[string]any); ok {
			publishedPayload = typed
		}
		return nil
	})

	record := RecordChoiceUsage(ctx, state, Record{
		NodeID:     "Planner_test",
		Model:      "gpt-test",
		StateScope: "agent",
	}, &llms.ContentChoice{
		StopReason: "stop",
		GenerationInfo: map[string]any{
			"prompt_tokens":        11,
			"completion_tokens":    7,
			"reasoning_tokens":     3,
			"prompt_cached_tokens": 2,
		},
	})

	if record.TotalTokens != 18 {
		t.Fatalf("record.TotalTokens = %d, want 18", record.TotalTokens)
	}
	if publishedType != runtime.EventLLMUsage {
		t.Fatalf("publishedType = %q, want %q", publishedType, runtime.EventLLMUsage)
	}
	if got := publishedPayload["calls"]; got != 1 {
		t.Fatalf("payload calls = %#v, want 1", got)
	}
	if got := publishedPayload["node_id"]; got != "Planner_test" {
		t.Fatalf("payload node_id = %#v, want %q", got, "Planner_test")
	}
	if got := publishedPayload["total_tokens"]; got != 18 {
		t.Fatalf("payload total_tokens = %#v, want 18", got)
	}

	usageState := state.Get(TokenUsageStateKey)
	if usageState == nil {
		t.Fatalf("state[%q] missing", TokenUsageStateKey)
	}
	totals, _ := usageState["totals"].(map[string]any)
	if totals == nil {
		t.Fatalf("usage totals missing")
	}
	if got := totals["calls"]; got != 1 {
		t.Fatalf("totals calls = %#v, want 1", got)
	}
	if got := totals["prompt_tokens"]; got != 11 {
		t.Fatalf("totals prompt_tokens = %#v, want 11", got)
	}
	if got := totals["total_tokens"]; got != 18 {
		t.Fatalf("totals total_tokens = %#v, want 18", got)
	}
}
