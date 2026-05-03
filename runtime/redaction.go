package runtime

import (
	"strings"
	"weaveflow/redact"
)

func RedactMessages(messages []StateMessage) []StateMessage {
	if len(messages) == 0 {
		return nil
	}

	redacted := make([]StateMessage, len(messages))
	for i, message := range messages {
		redacted[i] = StateMessage{
			Role:  message.Role,
			Parts: redactMessageParts(message.Parts),
		}
	}
	return redacted
}

func redactMessageParts(parts []StateMessagePart) []StateMessagePart {
	if len(parts) == 0 {
		return nil
	}

	redacted := make([]StateMessagePart, len(parts))
	for i, part := range parts {
		copyPart := part
		copyPart.Text = redact.Text(copyPart.Text)
		copyPart.URL = redact.Text(copyPart.URL)
		copyPart.Data = redactBinaryData(copyPart.Data)
		copyPart.Arguments = redact.JSONString(copyPart.Arguments)
		copyPart.Content = redact.Text(copyPart.Content)
		redacted[i] = copyPart
	}
	return redacted
}

func redactBinaryData(data string) string {
	if strings.TrimSpace(data) == "" {
		return ""
	}
	return redact.Placeholder
}
