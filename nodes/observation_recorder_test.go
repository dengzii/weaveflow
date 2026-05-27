package nodes

import (
	"context"
	"strings"
	"testing"
	wfstate "weaveflow/state"

	"github.com/tmc/langchaingo/llms"
)

func TestObservationRecorderIncludesToolOutputsBeforeSynthesis(t *testing.T) {
	t.Parallel()

	state := wfstate.State{
		wfstate.StateKeyPlanner: map[string]any{
			"current_step_id": "step_1",
		},
	}
	state.Conversation("agent").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "inspect state design"),
		{
			Role: llms.ChatMessageTypeAI,
			Parts: []llms.ContentPart{
				testToolCall("call_1", "read", `{"file_path":"state/keys.go"}`),
			},
		},
		{
			Role: llms.ChatMessageTypeTool,
			Parts: []llms.ContentPart{
				llms.ToolCallResponse{
					ToolCallID: "call_1",
					Name:       "read",
					Content:    `{"path":"state/keys.go","content":"StateKeyPlanner = \"planner\""}`,
				},
			},
		},
		llms.TextParts(llms.ChatMessageTypeAI, "The planner key is StateKeyPlanner."),
	})

	node := NewObservationRecorderNode()
	node.StateScope = "agent"
	next, err := runTestNode(t, node, context.Background(), state)
	if err != nil {
		t.Fatalf("invoke observation recorder: %v", err)
	}

	observations := filterObservationsByStep(next.Observations(), "step_1")
	if len(observations) != 2 {
		t.Fatalf("expected synthesized answer and tool output observations, got %#v", observations)
	}
	if observations[0]["source"] != "llm" {
		t.Fatalf("expected latest AI observation first, got %#v", observations[0])
	}
	if observations[1]["source"] != "tool:read" {
		t.Fatalf("expected preceding tool observation, got %#v", observations[1])
	}
	if observations[1]["summary"] == "" {
		t.Fatalf("expected tool summary to be recorded, got %#v", observations[1])
	}
}

func TestObservationRecorderKeepsLongLLMStepSummaryForVerification(t *testing.T) {
	t.Parallel()

	longResult := strings.Repeat("状态设计发现：State uses root keys, scoped namespaces, and path helpers. ", 80)
	state := wfstate.State{
		wfstate.StateKeyPlanner: map[string]any{
			"current_step_id": "step_1",
		},
	}
	state.Conversation("agent").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeAI, longResult),
	})

	node := NewObservationRecorderNode()
	node.StateScope = "agent"
	next, err := runTestNode(t, node, context.Background(), state)
	if err != nil {
		t.Fatalf("invoke observation recorder: %v", err)
	}

	observations := filterObservationsByStep(next.Observations(), "step_1")
	if len(observations) != 1 {
		t.Fatalf("expected one LLM observation, got %#v", observations)
	}
	summary, _ := observations[0]["summary"].(string)
	if len(summary) <= observationMaxToolSummaryLen {
		t.Fatalf("expected LLM summary to exceed tool summary cap, got len=%d", len(summary))
	}
	if len(summary) > observationMaxLLMSummaryLen+3 {
		t.Fatalf("expected LLM summary to respect LLM cap, got len=%d", len(summary))
	}
	if observations[0]["truncated"] != true {
		t.Fatalf("expected long LLM summary to be marked truncated, got %#v", observations[0])
	}
}

func TestObservationRecorderMarksToolExecutionFailedAsError(t *testing.T) {
	t.Parallel()

	state := wfstate.State{
		wfstate.StateKeyPlanner: map[string]any{
			"current_step_id": "step_1",
		},
	}
	state.Conversation("agent").UpdateMessage([]llms.MessageContent{
		{
			Role: llms.ChatMessageTypeAI,
			Parts: []llms.ContentPart{
				testToolCall("call_1", "read", `{"file_path":"missing.go"}`),
			},
		},
		{
			Role: llms.ChatMessageTypeTool,
			Parts: []llms.ContentPart{
				llms.ToolCallResponse{
					ToolCallID: "call_1",
					Name:       "read",
					Content:    "tool execution failed: file not found",
				},
			},
		},
	})

	node := NewObservationRecorderNode()
	node.StateScope = "agent"
	next, err := runTestNode(t, node, context.Background(), state)
	if err != nil {
		t.Fatalf("invoke observation recorder: %v", err)
	}

	observations := filterObservationsByStep(next.Observations(), "step_1")
	if len(observations) != 1 {
		t.Fatalf("expected one tool observation, got %#v", observations)
	}
	if observations[0]["error"] == nil {
		t.Fatalf("expected tool execution failure to be marked as error, got %#v", observations[0])
	}
}

func TestIsToolErrorContentOnlyFlagsExplicitErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "tool execution failure prefix",
			content: "tool execution failed: file not found",
			want:    true,
		},
		{
			name:    "error prefix",
			content: "error: request failed",
			want:    true,
		},
		{
			name:    "json non-empty error string",
			content: `{"error":"request failed"}`,
			want:    true,
		},
		{
			name:    "json error null",
			content: `{"error":null,"results":[{"title":"ok"}]}`,
			want:    false,
		},
		{
			name:    "json empty error string",
			content: `{"error":"","content":"ok"}`,
			want:    false,
		},
		{
			name:    "plain content mentions error key",
			content: `The API schema includes "error": string for failed responses.`,
			want:    false,
		},
		{
			name:    "json content contains error key text",
			content: `{"content":"The API schema includes \"error\": string for failed responses."}`,
			want:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := isToolErrorContent(tc.content); got != tc.want {
				t.Fatalf("isToolErrorContent() = %v, want %v", got, tc.want)
			}
		})
	}
}
