package neo

import (
	"encoding/json"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

type orchestrationDisplay struct {
	Mode         string `json:"mode"`
	Reasoning    string `json:"reasoning"`
	DirectAnswer string `json:"direct_answer"`
}

func sanitizeHistoryMessages(messages []HistoryMessage) []HistoryMessage {
	if len(messages) == 0 {
		return messages
	}

	sanitized := make([]HistoryMessage, len(messages))
	for i, message := range messages {
		sanitized[i] = sanitizeHistoryMessage(message)
	}
	return sanitized
}

func sanitizeHistoryMessage(message HistoryMessage) HistoryMessage {
	role := strings.TrimSpace(message.Role)
	if role == string(llms.ChatMessageTypeHuman) || role == string(llms.ChatMessageTypeSystem) {
		return message
	}

	sanitizedParts := make([]MessagePart, 0, len(message.Parts)+1)
	directAnswer := ""
	for _, part := range message.Parts {
		switch part.Type {
		case "thinking":
			display, ok := parseOrchestrationDisplay(part.Text)
			if !ok {
				sanitizedParts = append(sanitizedParts, part)
				continue
			}

			if directAnswer == "" {
				directAnswer = display.DirectAnswer
			}
			if reasoning := strings.TrimSpace(display.Reasoning); reasoning != "" {
				part.Text = reasoning
				sanitizedParts = append(sanitizedParts, part)
			}

		case "text":
			display, ok := parseOrchestrationDisplay(part.Text)
			if !ok {
				sanitizedParts = append(sanitizedParts, part)
				continue
			}

			if directAnswer == "" {
				directAnswer = display.DirectAnswer
			}

			text := strings.TrimSpace(display.DirectAnswer)
			if text == "" {
				text = strings.TrimSpace(display.Reasoning)
			}
			if text == "" {
				continue
			}

			part.Text = text
			sanitizedParts = append(sanitizedParts, part)

		default:
			sanitizedParts = append(sanitizedParts, part)
		}
	}

	if directAnswer != "" && !historyPartsContainText(sanitizedParts, directAnswer) {
		sanitizedParts = append(sanitizedParts, MessagePart{Type: "text", Text: directAnswer})
	}

	message.Parts = sanitizedParts
	return message
}

func historyPartsContainText(parts []MessagePart, text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}

	for _, part := range parts {
		if part.Type == "text" && strings.TrimSpace(part.Text) == text {
			return true
		}
	}
	return false
}

func parseOrchestrationDisplay(text string) (orchestrationDisplay, bool) {
	candidate := extractOrchestrationJSON(text)
	if candidate == "" {
		return orchestrationDisplay{}, false
	}

	var display orchestrationDisplay
	if err := json.Unmarshal([]byte(candidate), &display); err != nil {
		return orchestrationDisplay{}, false
	}

	display.Mode = strings.ToLower(strings.TrimSpace(display.Mode))
	switch display.Mode {
	case "direct", "planner", "supervisor":
	default:
		return orchestrationDisplay{}, false
	}

	display.Reasoning = strings.TrimSpace(display.Reasoning)
	display.DirectAnswer = strings.TrimSpace(display.DirectAnswer)
	if display.Reasoning == "" && display.DirectAnswer == "" {
		return orchestrationDisplay{}, false
	}

	return display, true
}

func extractOrchestrationJSON(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}

	trimmed = stripHistoryJSONFence(trimmed)
	if strings.HasPrefix(trimmed, "{") {
		if candidate := extractBalancedJSONObject(trimmed, 0); candidate != "" {
			return candidate
		}
	}

	markers := []string{
		`{"mode"`,
		`{"direct_answer"`,
		`{"reasoning"`,
	}
	best := -1
	for _, marker := range markers {
		index := strings.Index(trimmed, marker)
		if index < 0 {
			continue
		}
		if best < 0 || index < best {
			best = index
		}
	}
	if best < 0 {
		return ""
	}

	return extractBalancedJSONObject(trimmed, best)
}

func stripHistoryJSONFence(text string) string {
	if !strings.HasPrefix(text, "```") {
		return text
	}
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```JSON")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	return strings.TrimSpace(text)
}

func extractBalancedJSONObject(text string, start int) string {
	if start < 0 || start >= len(text) || text[start] != '{' {
		return ""
	}

	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(text); i++ {
		ch := text[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch ch {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}

		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return strings.TrimSpace(text[start : i+1])
			}
		}
	}

	return ""
}
