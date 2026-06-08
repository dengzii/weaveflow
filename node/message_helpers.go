package node

import (
	"fmt"
	"strings"

	"weaveflow/llms/parts"
	"weaveflow/state"

	"github.com/tmc/langchaingo/llms"
)

func extractText(message llms.MessageContent) string {
	texts := make([]string, 0, len(message.Parts))
	for _, part := range message.Parts {
		if textPart, ok := part.(llms.TextContent); ok {
			text := strings.TrimSpace(textPart.Text)
			if text != "" {
				texts = append(texts, text)
			}
		}
	}
	return strings.Join(texts, "\n")
}

func cloneMessages(messages []llms.MessageContent) []llms.MessageContent {
	if len(messages) == 0 {
		return nil
	}
	cloned := make([]llms.MessageContent, len(messages))
	for i, message := range messages {
		cloned[i] = llms.MessageContent{
			Role:  message.Role,
			Parts: append([]llms.ContentPart(nil), message.Parts...),
		}
	}
	return cloned
}

func splitReducerMessages(messages []llms.MessageContent, preserveSystem bool, summaryPrefix string) ([]llms.MessageContent, []llms.MessageContent) {
	if !preserveSystem || len(messages) == 0 {
		return nil, cloneMessages(messages)
	}

	index := 0
	for index < len(messages) {
		if messages[index].Role != llms.ChatMessageTypeSystem {
			break
		}
		if isReducerSummaryMessage(messages[index], summaryPrefix) {
			break
		}
		index++
	}
	return cloneMessages(messages[:index]), cloneMessages(messages[index:])
}

func adjustReducerTailStart(messages []llms.MessageContent, start int) int {
	if start <= 0 || start >= len(messages) {
		return start
	}
	for start > 0 {
		current := messages[start]
		previous := messages[start-1]
		if current.Role == llms.ChatMessageTypeTool || previous.Role == llms.ChatMessageTypeTool || messageHasToolCalls(previous) {
			start--
			continue
		}
		break
	}
	return start
}

func buildReducerTranscript(messages []llms.MessageContent) string {
	lines := make([]string, 0, len(messages))
	for _, message := range messages {
		text := strings.TrimSpace(reducerMessageText(message))
		if text == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s: %s", reducerRoleLabel(message.Role), text))
	}
	return strings.Join(lines, "\n\n")
}

func reducerMessageText(message llms.MessageContent) string {
	texts := make([]string, 0, len(message.Parts))
	for _, part := range message.Parts {
		switch typed := part.(type) {
		case llms.TextContent:
			text := strings.TrimSpace(typed.Text)
			if text != "" {
				texts = append(texts, text)
			}
		case parts.ReasoningPart:
			text := strings.TrimSpace(typed.Text)
			if text != "" {
				texts = append(texts, text)
			}
		case llms.ToolCall:
			name := ""
			arguments := ""
			if typed.FunctionCall != nil {
				name = strings.TrimSpace(typed.FunctionCall.Name)
				arguments = strings.TrimSpace(typed.FunctionCall.Arguments)
			}
			item := "tool_call"
			if name != "" {
				item += " " + name
			}
			if arguments != "" {
				item += " " + arguments
			}
			texts = append(texts, item)
		case llms.ToolCallResponse:
			item := "tool_result"
			if name := strings.TrimSpace(typed.Name); name != "" {
				item += " " + name
			}
			if content := strings.TrimSpace(typed.Content); content != "" {
				item += " " + content
			}
			texts = append(texts, item)
		}
	}
	return strings.Join(texts, "\n")
}

func reducerRoleLabel(role llms.ChatMessageType) string {
	switch role {
	case llms.ChatMessageTypeSystem:
		return "system"
	case llms.ChatMessageTypeHuman:
		return "human"
	case llms.ChatMessageTypeAI:
		return "assistant"
	case llms.ChatMessageTypeTool:
		return "tool"
	default:
		return strings.TrimSpace(string(role))
	}
}

func isReducerSummaryMessage(message llms.MessageContent, summaryPrefix string) bool {
	if message.Role != llms.ChatMessageTypeSystem {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(extractText(message)), strings.TrimSpace(summaryPrefix))
}

func messageHasToolCalls(message llms.MessageContent) bool {
	for _, part := range message.Parts {
		if _, ok := part.(llms.ToolCall); ok {
			return true
		}
	}
	return false
}

func messageHasToolResponses(message llms.MessageContent) bool {
	for _, part := range message.Parts {
		if _, ok := part.(llms.ToolCallResponse); ok {
			return true
		}
	}
	return false
}

func trimLLMPromptMessages(messages []llms.MessageContent, maxChars int) []llms.MessageContent {
	if len(messages) == 0 || maxChars <= 0 {
		return cloneMessages(messages)
	}
	if promptMessagesCharCount(messages) <= maxChars {
		return cloneMessages(messages)
	}

	leadingSystem, body := splitLeadingSystemMessages(messages)
	prefix := cloneMessages(leadingSystem)
	if promptMessagesCharCount(prefix) > maxChars && len(prefix) > 1 {
		prefix = prefix[:1]
	}

	firstHumanIdx := -1
	for i, message := range body {
		if message.Role == llms.ChatMessageTypeHuman {
			firstHumanIdx = i
			break
		}
	}
	if firstHumanIdx >= 0 {
		pinned := body[firstHumanIdx]
		body = append(body[:firstHumanIdx:firstHumanIdx], body[firstHumanIdx+1:]...)
		prefix = append(prefix, pinned)
	}

	used := promptMessagesCharCount(prefix)
	if len(body) == 0 {
		return prefix
	}

	start := len(body)
	for i := len(body) - 1; i >= 0; i-- {
		candidateCost := promptMessageCharCount(body[i])
		if used+candidateCost > maxChars && start < len(body) {
			break
		}
		if used+candidateCost > maxChars {
			start = i
			break
		}
		used += candidateCost
		start = i
	}

	if start > 0 && start < len(body) {
		adjusted := adjustReducerTailStart(body, start)
		candidate := append(cloneMessages(prefix), body[adjusted:]...)
		if promptMessagesCharCount(candidate) <= maxChars {
			start = adjusted
		}
	}
	if start >= len(body) {
		start = adjustReducerTailStart(body, len(body)-1)
	}

	result := make([]llms.MessageContent, 0, len(prefix)+len(body[start:]))
	result = append(result, prefix...)
	result = append(result, cloneMessages(body[start:])...)
	return result
}

func splitLeadingSystemMessages(messages []llms.MessageContent) ([]llms.MessageContent, []llms.MessageContent) {
	index := 0
	for index < len(messages) && messages[index].Role == llms.ChatMessageTypeSystem {
		index++
	}
	return cloneMessages(messages[:index]), cloneMessages(messages[index:])
}

func promptMessagesCharCount(messages []llms.MessageContent) int {
	total := 0
	for _, message := range messages {
		total += promptMessageCharCount(message)
	}
	return total
}

func promptMessageCharCount(message llms.MessageContent) int {
	total := len(string(message.Role)) + 8
	for _, part := range message.Parts {
		switch typed := part.(type) {
		case llms.TextContent:
			total += len([]rune(strings.TrimSpace(typed.Text)))
		case parts.ReasoningPart:
			total += len([]rune(strings.TrimSpace(typed.Text)))
		case llms.ToolCall:
			total += 16
			if typed.FunctionCall != nil {
				total += len([]rune(strings.TrimSpace(typed.FunctionCall.Name)))
				total += len([]rune(strings.TrimSpace(typed.FunctionCall.Arguments)))
			}
		case llms.ToolCallResponse:
			total += 16
			total += len([]rune(strings.TrimSpace(typed.Name)))
			total += len([]rune(strings.TrimSpace(typed.Content)))
		default:
			total += len([]rune(strings.TrimSpace(fmt.Sprint(typed))))
		}
	}
	return total
}

type llmPromptArtifact struct {
	StateScope     string               `json:"state_scope,omitempty"`
	IterationCount int                  `json:"iteration_count,omitempty"`
	MaxIterations  int                  `json:"max_iterations,omitempty"`
	Messages       []state.StateMessage `json:"messages,omitempty"`
	Tools          []llmToolArtifact    `json:"tools,omitempty"`
}

type llmToolArtifact struct {
	Type     string                   `json:"type,omitempty"`
	Function *llms.FunctionDefinition `json:"function,omitempty"`
}

type llmResponseArtifact struct {
	Choices []llmResponseArtifactChoice `json:"choices,omitempty"`
}

type llmResponseArtifactChoice struct {
	Content          string             `json:"content,omitempty"`
	StopReason       string             `json:"stop_reason,omitempty"`
	ToolCalls        []llms.ToolCall    `json:"tool_calls,omitempty"`
	FunctionCall     *llms.FunctionCall `json:"function_call,omitempty"`
	ReasoningContent string             `json:"reasoning_content,omitempty"`
	Usage            map[string]any     `json:"usage,omitempty"`
}

func buildLLMPromptArtifact(messages []llms.MessageContent, tools []llms.Tool, stateScope string, iterationCount int, maxIterations int) (llmPromptArtifact, error) {
	serializedMessages, err := state.SerializeMessages(messages)
	if err != nil {
		return llmPromptArtifact{}, err
	}

	payload := llmPromptArtifact{
		StateScope:     stateScope,
		IterationCount: iterationCount,
		MaxIterations:  maxIterations,
		Messages:       serializedMessages,
	}
	if len(tools) > 0 {
		payload.Tools = make([]llmToolArtifact, 0, len(tools))
		for _, tool := range tools {
			payload.Tools = append(payload.Tools, llmToolArtifact{
				Type:     tool.Type,
				Function: tool.Function,
			})
		}
	}
	return payload, nil
}

func buildLLMResponseArtifact(resp *llms.ContentResponse) llmResponseArtifact {
	if resp == nil || len(resp.Choices) == 0 {
		return llmResponseArtifact{}
	}

	payload := llmResponseArtifact{
		Choices: make([]llmResponseArtifactChoice, 0, len(resp.Choices)),
	}
	for _, choice := range resp.Choices {
		if choice == nil {
			continue
		}
		item := llmResponseArtifactChoice{
			Content:          choice.Content,
			StopReason:       choice.StopReason,
			ReasoningContent: choice.ReasoningContent,
		}
		if choice.FuncCall != nil {
			copyCall := *choice.FuncCall
			item.FunctionCall = &copyCall
		}
		if len(choice.ToolCalls) > 0 {
			item.ToolCalls = redactToolCalls(choice.ToolCalls)
		}
		payload.Choices = append(payload.Choices, item)
	}
	return payload
}

func redactToolCalls(toolCalls []llms.ToolCall) []llms.ToolCall {
	if len(toolCalls) == 0 {
		return nil
	}

	redacted := make([]llms.ToolCall, len(toolCalls))
	for i, toolCall := range toolCalls {
		redacted[i] = toolCall
		if toolCall.FunctionCall == nil {
			continue
		}
		copyCall := *toolCall.FunctionCall
		redacted[i].FunctionCall = &copyCall
	}
	return redacted
}
