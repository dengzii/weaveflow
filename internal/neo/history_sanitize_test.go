package neo

import (
	"testing"

	"github.com/tmc/langchaingo/llms"
)

func TestSanitizeHistoryMessagePromotesDirectAnswerFromThinking(t *testing.T) {
	t.Parallel()

	message := HistoryMessage{
		Role: string(llms.ChatMessageTypeAI),
		Parts: []MessagePart{
			{
				Type: "thinking",
				Text: `{"mode":"direct","reasoning":"A simple acknowledgement is enough.","direct_answer":"No problem."}`,
			},
		},
	}

	got := sanitizeHistoryMessage(message)
	if len(got.Parts) != 2 {
		t.Fatalf("sanitizeHistoryMessage() parts len = %d, want 2", len(got.Parts))
	}
	if got.Parts[0].Type != "thinking" || got.Parts[0].Text != "A simple acknowledgement is enough." {
		t.Fatalf("thinking part = %#v", got.Parts[0])
	}
	if got.Parts[1].Type != "text" || got.Parts[1].Text != "No problem." {
		t.Fatalf("assistant part = %#v", got.Parts[1])
	}
}

func TestStoreLoadLLMMessagesSanitizesOrchestrationJSON(t *testing.T) {
	store := newTestStore(t)

	history := []HistoryMessage{
		{
			Role:  string(llms.ChatMessageTypeHuman),
			Parts: []MessagePart{{Type: "text", Text: "hi"}},
		},
		{
			Role: string(llms.ChatMessageTypeAI),
			Parts: []MessagePart{
				{
					Type: "text",
					Text: `{"mode":"direct","reasoning":"A simple acknowledgement is enough.","direct_answer":"No problem."}`,
				},
			},
		},
	}
	if err := store.SaveRawHistory(defaultSessionID, history, "completed"); err != nil {
		t.Fatalf("SaveRawHistory() error = %v", err)
	}

	msgs, err := store.LoadLLMMessages(defaultSessionID)
	if err != nil {
		t.Fatalf("LoadLLMMessages() error = %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("LoadLLMMessages() len = %d, want 2", len(msgs))
	}

	if len(msgs[1].Parts) != 1 {
		t.Fatalf("assistant parts len = %d, want 1", len(msgs[1].Parts))
	}
	textPart, ok := msgs[1].Parts[0].(llms.TextContent)
	if !ok {
		t.Fatalf("assistant part type = %T, want llms.TextContent", msgs[1].Parts[0])
	}
	if textPart.Text != "No problem." {
		t.Fatalf("assistant text = %q, want %q", textPart.Text, "No problem.")
	}
}
