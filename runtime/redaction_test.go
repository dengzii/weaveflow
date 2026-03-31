package runtime

import (
	"testing"

	"falcon/internal/redact"
)

func TestRedactMessages(t *testing.T) {
	t.Parallel()

	messages := []StateMessage{
		{
			Role: "user",
			Parts: []StateMessagePart{
				{
					Kind:      "text",
					Text:      "Authorization: Bearer abc",
					Arguments: `{"api_key":"secret-value"}`,
				},
			},
		},
	}

	redactedMessages := RedactMessages(messages)
	if len(redactedMessages) != 1 || len(redactedMessages[0].Parts) != 1 {
		t.Fatalf("unexpected redacted messages shape: %#v", redactedMessages)
	}

	part := redactedMessages[0].Parts[0]
	if part.Text != redact.Placeholder {
		t.Fatalf("expected redacted text, got %q", part.Text)
	}
	if part.Arguments == `{"api_key":"secret-value"}` {
		t.Fatalf("expected arguments to be redacted, got %q", part.Arguments)
	}
}
