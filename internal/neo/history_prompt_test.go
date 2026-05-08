package neo

import (
	"strings"
	"testing"

	"github.com/tmc/langchaingo/llms"
)

func TestBuildLLMHistoryAddsSummaryAndKeepsRecentTurns(t *testing.T) {
	t.Parallel()

	history := make([]HistoryMessage, 0, 16)
	for i := 1; i <= 8; i++ {
		history = append(history,
			HistoryMessage{
				Role:  string(llms.ChatMessageTypeHuman),
				Parts: []MessagePart{{Type: "text", Text: "user turn " + string(rune('0'+i))}},
			},
			HistoryMessage{
				Role:  string(llms.ChatMessageTypeAI),
				Parts: []MessagePart{{Type: "text", Text: "assistant turn " + string(rune('0'+i))}},
			},
		)
	}

	messages := buildLLMHistory(history)
	if len(messages) != 13 {
		t.Fatalf("buildLLMHistory() len = %d, want 13", len(messages))
	}
	if messages[0].Role != llms.ChatMessageTypeSystem {
		t.Fatalf("expected summary system message, got %#v", messages[0])
	}
	summary := messageText(messages[0])
	if !strings.Contains(summary, "user turn 1") || !strings.Contains(summary, "assistant turn 2") {
		t.Fatalf("unexpected summary text: %q", summary)
	}
	if got := messageText(messages[1]); got != "user turn 3" {
		t.Fatalf("expected recent window to start at user turn 3, got %q", got)
	}
	if got := messageText(messages[len(messages)-1]); got != "assistant turn 8" {
		t.Fatalf("expected latest assistant turn to be preserved, got %q", got)
	}
}

func TestBuildLLMHistoryPreservesLeadingSystemMessages(t *testing.T) {
	t.Parallel()

	history := []HistoryMessage{
		{
			Role:  string(llms.ChatMessageTypeSystem),
			Parts: []MessagePart{{Type: "text", Text: "System prompt"}},
		},
		{
			Role:  string(llms.ChatMessageTypeHuman),
			Parts: []MessagePart{{Type: "text", Text: "hello"}},
		},
		{
			Role:  string(llms.ChatMessageTypeAI),
			Parts: []MessagePart{{Type: "text", Text: "world"}},
		},
	}

	messages := buildLLMHistory(history)
	if len(messages) != 3 {
		t.Fatalf("buildLLMHistory() len = %d, want 3", len(messages))
	}
	if messages[0].Role != llms.ChatMessageTypeSystem || messageText(messages[0]) != "System prompt" {
		t.Fatalf("expected leading system message to be preserved, got %#v", messages[0])
	}
}
