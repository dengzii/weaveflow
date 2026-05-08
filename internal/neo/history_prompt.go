package neo

import (
	"fmt"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

const (
	defaultPromptRecentTurns      = 6
	defaultPromptSummaryMaxChars  = 1800
	defaultPromptSummaryHeading   = "Summary of earlier conversation:"
	defaultPromptSummaryOmitLabel = "- Earlier details omitted for brevity."
)

type PromptHistoryOptions struct {
	RecentTurns     int
	SummaryMaxChars int
}

func buildLLMHistory(messages []HistoryMessage) []llms.MessageContent {
	return buildLLMHistoryWithOptions(messages, PromptHistoryOptions{})
}

func buildLLMHistoryWithOptions(messages []HistoryMessage, options PromptHistoryOptions) []llms.MessageContent {
	if len(messages) == 0 {
		return nil
	}
	options = normalizePromptHistoryOptions(options)

	leadingSystem, body := splitLeadingSystemHistory(messages)
	older, recent := splitRecentHistoryTurns(body, options.RecentTurns)

	result := make([]llms.MessageContent, 0, len(leadingSystem)+len(recent)+1)
	for _, message := range leadingSystem {
		appendLLMHistoryMessage(&result, message)
	}
	if summary := buildHistorySummaryMessage(older, options.SummaryMaxChars); summary != nil {
		result = append(result, *summary)
	}
	for _, message := range recent {
		appendLLMHistoryMessage(&result, message)
	}

	return result
}

func normalizePromptHistoryOptions(options PromptHistoryOptions) PromptHistoryOptions {
	if options.RecentTurns <= 0 {
		options.RecentTurns = defaultPromptRecentTurns
	}
	if options.SummaryMaxChars <= 0 {
		options.SummaryMaxChars = defaultPromptSummaryMaxChars
	}
	return options
}

func appendLLMHistoryMessage(result *[]llms.MessageContent, message HistoryMessage) {
	llmMessage := historyToLLM(message)
	if !llmMessageHasContent(llmMessage) {
		return
	}
	*result = append(*result, llmMessage)
}

func llmMessageHasContent(message llms.MessageContent) bool {
	if len(message.Parts) == 0 {
		return false
	}
	for _, part := range message.Parts {
		switch typed := part.(type) {
		case llms.TextContent:
			if strings.TrimSpace(typed.Text) != "" {
				return true
			}
		case llms.ToolCall:
			return true
		case llms.ToolCallResponse:
			return true
		}
	}
	return false
}

func splitLeadingSystemHistory(messages []HistoryMessage) ([]HistoryMessage, []HistoryMessage) {
	index := 0
	for index < len(messages) && strings.TrimSpace(messages[index].Role) == string(llms.ChatMessageTypeSystem) {
		index++
	}
	leading := append([]HistoryMessage(nil), messages[:index]...)
	body := append([]HistoryMessage(nil), messages[index:]...)
	return leading, body
}

func splitRecentHistoryTurns(messages []HistoryMessage, recentTurns int) ([]HistoryMessage, []HistoryMessage) {
	if len(messages) == 0 {
		return nil, nil
	}
	if recentTurns <= 0 {
		return append([]HistoryMessage(nil), messages...), nil
	}

	humanTurns := 0
	start := len(messages)
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.TrimSpace(messages[i].Role) == string(llms.ChatMessageTypeHuman) {
			humanTurns++
		}
		start = i
		if humanTurns >= recentTurns {
			break
		}
	}
	if humanTurns < recentTurns {
		return nil, append([]HistoryMessage(nil), messages...)
	}

	older := append([]HistoryMessage(nil), messages[:start]...)
	recent := append([]HistoryMessage(nil), messages[start:]...)
	return older, recent
}

func buildHistorySummaryMessage(messages []HistoryMessage, maxChars int) *llms.MessageContent {
	lines := buildHistorySummaryLines(messages, maxChars)
	if len(lines) == 0 {
		return nil
	}
	content := defaultPromptSummaryHeading + "\n" + strings.Join(lines, "\n")
	message := llms.TextParts(llms.ChatMessageTypeSystem, content)
	return &message
}

func buildHistorySummaryLines(messages []HistoryMessage, maxChars int) []string {
	if len(messages) == 0 {
		return nil
	}
	if maxChars <= 0 {
		maxChars = defaultPromptSummaryMaxChars
	}

	rawLines := make([]string, 0, len(messages))
	for _, message := range messages {
		line := historySummaryLine(message)
		if line == "" {
			continue
		}
		rawLines = append(rawLines, line)
	}
	if len(rawLines) == 0 {
		return nil
	}

	remaining := maxChars
	selected := make([]string, 0, len(rawLines))
	for i := len(rawLines) - 1; i >= 0; i-- {
		line := rawLines[i]
		cost := len([]rune(line))
		if len(selected) > 0 {
			cost++
		}
		if cost > remaining {
			break
		}
		selected = append(selected, line)
		remaining -= cost
	}
	if len(selected) == 0 {
		return nil
	}

	for i, j := 0, len(selected)-1; i < j; i, j = i+1, j-1 {
		selected[i], selected[j] = selected[j], selected[i]
	}
	if len(selected) < len(rawLines) {
		selected = append([]string{defaultPromptSummaryOmitLabel}, selected...)
	}
	return selected
}

func historySummaryLine(message HistoryMessage) string {
	text := historySummaryText(message)
	if text == "" {
		return ""
	}
	return fmt.Sprintf("- %s: %s", historyRoleLabel(message.Role), clipHistoryText(text, 240))
}

func historySummaryText(message HistoryMessage) string {
	parts := make([]string, 0, len(message.Parts))
	hasText := false
	for _, part := range message.Parts {
		switch part.Type {
		case "text":
			text := normalizeHistoryWhitespace(part.Text)
			if text == "" {
				continue
			}
			hasText = true
			parts = append(parts, text)
		case "tool_result":
			text := normalizeHistoryWhitespace(part.Result)
			if text == "" {
				continue
			}
			if name := strings.TrimSpace(part.Name); name != "" {
				parts = append(parts, fmt.Sprintf("tool %s => %s", name, text))
			} else {
				parts = append(parts, "tool result => "+text)
			}
		case "tool_call":
			if hasText {
				continue
			}
			name := strings.TrimSpace(part.Name)
			if name == "" {
				name = "tool"
			}
			arguments := normalizeHistoryWhitespace(part.Text)
			if arguments != "" {
				parts = append(parts, fmt.Sprintf("called %s %s", name, clipHistoryText(arguments, 120)))
			} else {
				parts = append(parts, "called "+name)
			}
		}
	}
	return strings.Join(parts, " | ")
}

func historyRoleLabel(role string) string {
	switch strings.TrimSpace(role) {
	case string(llms.ChatMessageTypeHuman):
		return "User"
	case string(llms.ChatMessageTypeAI):
		return "Assistant"
	case string(llms.ChatMessageTypeSystem):
		return "System"
	case string(llms.ChatMessageTypeTool):
		return "Tool"
	default:
		return "Message"
	}
}

func normalizeHistoryWhitespace(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}

func clipHistoryText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimSpace(string(runes[:limit])) + "..."
}
