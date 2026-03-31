package llama_cpp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

var (
	_ llms.Model          = (*Model)(nil)
	_ llms.ReasoningModel = (*Model)(nil)
)

func (m *Model) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	opts := llms.CallOptions{}
	for _, option := range options {
		option(&opts)
	}

	thinking := resolveThinkingSettings(m, &opts)

	prompt, err := buildPrompt(messages, collectPromptTools(opts), thinking)
	if err != nil {
		return nil, err
	}

	generateCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	resultCh, errCh := m.Generate(generateCtx, prompt, generateOptionsFromCallOptions(opts))

	var builder strings.Builder
	finalResult := GenerateResult{}
	var streamErr error
	streamParser := newThinkStreamParser()

	for result := range resultCh {
		if result.Content != "" {
			builder.WriteString(result.Content)

			if streamErr == nil {
				reasoningChunk, contentChunk := streamParser.Write(result.Content, false)
				streamErr = emitStreaming(ctx, opts, thinking, reasoningChunk, contentChunk)
				if streamErr != nil {
					cancel()
				}
			}
		}

		if result.TokenCount > 0 {
			finalResult.TokenCount = result.TokenCount
		}
		if result.StopReason != StopReasonNone {
			finalResult.StopReason = result.StopReason
		}
	}

	if err, ok := <-errCh; ok && err != nil {
		if streamErr != nil && errors.Is(err, context.Canceled) {
			return nil, streamErr
		}
		return nil, err
	}
	if streamErr != nil {
		return nil, streamErr
	}

	rawContent := strings.TrimSpace(builder.String())
	reasoningContent, outputContent := splitThinkingContent(rawContent)
	if outputContent == "" {
		outputContent = rawContent
	}

	choice := &llms.ContentChoice{
		Content:    outputContent,
		StopReason: finalResult.StopReason,
		ReasoningContent: func() string {
			if thinking.returnThinking {
				return reasoningContent
			}
			return ""
		}(),
		GenerationInfo: map[string]any{
			"CompletionTokens": finalResult.TokenCount,
			"TotalTokens":      finalResult.TokenCount,
			"StopReason":       finalResult.StopReason,
			"ThinkingContent": func() string {
				if thinking.returnThinking {
					return reasoningContent
				}
				return ""
			}(),
			"OutputContent":   outputContent,
			"ThinkingEnabled": thinking.enabled,
		},
	}

	if parsed, ok := parseStructuredResponse(outputContent); ok {
		if parsed.Content != "" {
			choice.Content = parsed.Content
		}
		if len(parsed.ToolCalls) > 0 {
			choice.ToolCalls = parsed.ToolCalls
			choice.FuncCall = parsed.ToolCalls[0].FunctionCall
		}
	}

	return &llms.ContentResponse{
		Choices: []*llms.ContentChoice{choice},
	}, nil
}

func (m *Model) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	return llms.GenerateFromSinglePrompt(ctx, m, prompt, options...)
}

func (m *Model) SupportsReasoning() bool {
	return true
	//name := strings.ToLower(strings.TrimSpace(m.Name()))
	//if name == "" {
	//	return false
	//}
	//
	//if strings.Contains(name, "rwkv") {
	//	return true
	//}
	//
	//return llms.DefaultIsReasoningModel(name) ||
	//	strings.Contains(name, "thinking") ||
	//	strings.Contains(name, "reasoning")
}

func generateOptionsFromCallOptions(opts llms.CallOptions) GenerateOptions {
	return GenerateOptions{
		MaxTokens:   opts.MaxTokens,
		Temperature: float32(opts.Temperature),
		TopP:        float32(opts.TopP),
		TopK:        opts.TopK,
		Seed:        uint32(opts.Seed),
		Stop:        append([]string(nil), opts.StopWords...),
	}
}

func collectPromptTools(opts llms.CallOptions) []llms.Tool {
	tools := make([]llms.Tool, 0, len(opts.Tools)+len(opts.Functions))
	for _, fn := range opts.Tools {
		if fn.Function == nil {
			continue
		}
		tools = append(tools, llms.Tool{
			Type: "function",
			Function: &llms.FunctionDefinition{
				Name:        fn.Function.Name,
				Description: fn.Function.Description,
				Parameters:  fn.Function.Parameters,
				Strict:      fn.Function.Strict,
			},
		})
	}
	for _, fn := range opts.Functions {
		tools = append(tools, llms.Tool{
			Type: "function",
			Function: &llms.FunctionDefinition{
				Name:        fn.Name,
				Description: fn.Description,
				Parameters:  fn.Parameters,
				Strict:      fn.Strict,
			},
		})
	}
	return tools
}

type thinkingSettings struct {
	enabled        bool
	returnThinking bool
	streamThinking bool
}

func resolveThinkingSettings(model *Model, opts *llms.CallOptions) thinkingSettings {
	settings := thinkingSettings{}
	config := llms.GetThinkingConfig(opts)
	if config == nil {
		return settings
	}

	settings.returnThinking = config.ReturnThinking
	settings.streamThinking = config.StreamThinking

	if config.Mode == llms.ThinkingModeNone && config.BudgetTokens <= 0 && !config.ReturnThinking && !config.StreamThinking {
		return settings
	}

	settings.enabled = model != nil && model.SupportsReasoning()
	return settings
}

func buildPrompt(messages []llms.MessageContent, tools []llms.Tool, thinking thinkingSettings) (string, error) {
	var builder strings.Builder

	if len(tools) > 0 {
		builder.WriteString("You can call tools. ")
		builder.WriteString("When a tool is needed, reply with JSON only in this format: ")
		builder.WriteString("{\"tool_calls\":[{\"name\":\"tool_name\",\"arguments\":{}}]}. ")
		builder.WriteString("When no tool is needed, reply with JSON only in this format: ")
		builder.WriteString("{\"content\":\"your answer\"}.\n\n")
		builder.WriteString("Available tools:\n")

		for _, tool := range tools {
			if tool.Function == nil {
				continue
			}

			builder.WriteString("- ")
			builder.WriteString(tool.Function.Name)
			if desc := strings.TrimSpace(tool.Function.Description); desc != "" {
				builder.WriteString(": ")
				builder.WriteString(desc)
			}
			if tool.Function.Parameters != nil {
				raw, err := json.Marshal(tool.Function.Parameters)
				if err != nil {
					return "", fmt.Errorf("marshal tool parameters for %q: %w", tool.Function.Name, err)
				}
				builder.WriteString("\n  parameters: ")
				builder.Write(raw)
			}
			builder.WriteByte('\n')
		}
		builder.WriteByte('\n')
	}

	if thinking.enabled {
		builder.WriteString("When solving the task, put your internal reasoning inside <think>...</think> tags. ")
		if thinking.returnThinking {
			builder.WriteString("Keep the final answer outside the think tags. ")
		} else {
			builder.WriteString("Keep the final answer concise and outside the think tags. ")
		}
		if len(tools) > 0 {
			builder.WriteString("If a tool is needed, place the JSON tool call after </think>.\n\n")
		} else {
			builder.WriteString("\n\n")
		}
	}

	for _, message := range messages {
		rendered, err := renderMessage(message)
		if err != nil {
			return "", err
		}
		if rendered == "" {
			continue
		}
		builder.WriteString(rendered)
		builder.WriteByte('\n')
	}

	builder.WriteString("Assistant:")
	if thinking.enabled {
		builder.WriteString(" <think>")
	}
	return builder.String(), nil
}

func renderMessage(message llms.MessageContent) (string, error) {
	role, err := promptRole(message.Role)
	if err != nil {
		return "", err
	}

	parts := make([]string, 0, len(message.Parts))
	for _, part := range message.Parts {
		rendered, err := renderPart(part)
		if err != nil {
			return "", err
		}
		if rendered != "" {
			parts = append(parts, rendered)
		}
	}

	if len(parts) == 0 {
		return "", nil
	}

	return role + ": " + strings.Join(parts, "\n"), nil
}

func promptRole(role llms.ChatMessageType) (string, error) {
	switch role {
	case llms.ChatMessageTypeSystem:
		return "System", nil
	case llms.ChatMessageTypeHuman, llms.ChatMessageTypeGeneric:
		return "User", nil
	case llms.ChatMessageTypeAI:
		return "Assistant", nil
	case llms.ChatMessageTypeFunction:
		return "Function", nil
	case llms.ChatMessageTypeTool:
		return "Tool", nil
	default:
		return "", fmt.Errorf("unsupported message role: %q", role)
	}
}

func renderPart(part llms.ContentPart) (string, error) {
	switch typed := part.(type) {
	case llms.TextContent:
		return strings.TrimSpace(typed.Text), nil
	case llms.ToolCall:
		if typed.FunctionCall == nil {
			return "", errors.New("tool call has no function payload")
		}

		payload := map[string]any{
			"id":   typed.ID,
			"type": typed.Type,
			"name": typed.FunctionCall.Name,
		}
		args, err := normalizeJSONObject(typed.FunctionCall.Arguments)
		if err != nil {
			payload["arguments"] = strings.TrimSpace(typed.FunctionCall.Arguments)
		} else {
			payload["arguments"] = args
		}

		raw, err := json.Marshal(payload)
		if err != nil {
			return "", err
		}
		return "tool_call=" + string(raw), nil
	case llms.ToolCallResponse:
		payload := map[string]any{
			"tool_call_id": typed.ToolCallID,
			"name":         typed.Name,
			"content":      typed.Content,
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return "", err
		}
		return "tool_result=" + string(raw), nil
	case llms.BinaryContent:
		return "", errors.New("binary content is not supported by llama_cpp text generation")
	case llms.ImageURLContent:
		return "", errors.New("image url content is not supported by llama_cpp text generation")
	default:
		return "", fmt.Errorf("unsupported message part type: %T", part)
	}
}

type structuredResponse struct {
	Content   string
	ToolCalls []llms.ToolCall
}

type thinkStreamParser struct {
	pending  string
	inThink  bool
	openTags []string
	endTags  []string
}

func newThinkStreamParser() *thinkStreamParser {
	return &thinkStreamParser{
		openTags: []string{"<think>", "<thinking>"},
		endTags:  []string{"</think>", "</thinking>"},
	}
}

func (p *thinkStreamParser) Write(chunk string, flush bool) (reasoning string, content string) {
	p.pending += chunk

	for {
		if p.inThink {
			index, tag := findFirstTag(p.pending, p.endTags)
			if index >= 0 {
				reasoning += p.pending[:index]
				p.pending = p.pending[index+len(tag):]
				p.inThink = false
				continue
			}

			if flush {
				reasoning += p.pending
				p.pending = ""
				return strings.TrimSpace(reasoning), strings.TrimSpace(content)
			}

			safe := len(p.pending) - maxTagLength(p.endTags) + 1
			if safe > 0 {
				reasoning += p.pending[:safe]
				p.pending = p.pending[safe:]
			}
			return reasoning, content
		}

		index, tag := findFirstTag(p.pending, p.openTags)
		if index >= 0 {
			content += p.pending[:index]
			p.pending = p.pending[index+len(tag):]
			p.inThink = true
			continue
		}

		if flush {
			content += p.pending
			p.pending = ""
			return strings.TrimSpace(reasoning), strings.TrimSpace(content)
		}

		safe := len(p.pending) - maxTagLength(p.openTags) + 1
		if safe > 0 {
			content += p.pending[:safe]
			p.pending = p.pending[safe:]
		}
		return reasoning, content
	}
}

func splitThinkingContent(raw string) (reasoning string, content string) {
	parser := newThinkStreamParser()
	return parser.Write(strings.TrimSpace(raw), true)
}

func emitStreaming(ctx context.Context, opts llms.CallOptions, thinking thinkingSettings, reasoningChunk string, contentChunk string) error {
	if opts.StreamingReasoningFunc != nil && (reasoningChunk != "" || contentChunk != "") {
		var reasoning []byte
		var content []byte
		if thinking.streamThinking && reasoningChunk != "" {
			reasoning = []byte(reasoningChunk)
		}
		if contentChunk != "" {
			content = []byte(contentChunk)
		}
		return opts.StreamingReasoningFunc(ctx, reasoning, content)
	}

	if opts.StreamingFunc != nil && contentChunk != "" {
		return opts.StreamingFunc(ctx, []byte(contentChunk))
	}

	return nil
}

func maxTagLength(tags []string) int {
	maxLen := 0
	for _, tag := range tags {
		if len(tag) > maxLen {
			maxLen = len(tag)
		}
	}
	return maxLen
}

func findFirstTag(input string, tags []string) (index int, tag string) {
	index = -1
	for _, candidate := range tags {
		current := strings.Index(input, candidate)
		if current < 0 {
			continue
		}
		if index < 0 || current < index {
			index = current
			tag = candidate
		}
	}
	return index, tag
}

func parseStructuredResponse(raw string) (structuredResponse, bool) {
	cleaned := trimMarkdownCodeFence(raw)
	if cleaned == "" {
		return structuredResponse{}, false
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(cleaned), &payload); err != nil {
		return structuredResponse{}, false
	}

	response := structuredResponse{
		Content: firstString(payload, "content", "answer", "response"),
	}

	if functionCall, ok := payload["function_call"]; ok {
		if toolCall, ok := parseFunctionCall(functionCall, "call_1"); ok {
			response.ToolCalls = append(response.ToolCalls, toolCall)
		}
	}

	if toolCalls, ok := payload["tool_calls"].([]any); ok {
		for index, entry := range toolCalls {
			if toolCall, ok := parseToolCall(entry, index+1); ok {
				response.ToolCalls = append(response.ToolCalls, toolCall)
			}
		}
	}

	return response, response.Content != "" || len(response.ToolCalls) > 0
}

func parseToolCall(value any, index int) (llms.ToolCall, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return llms.ToolCall{}, false
	}

	id := asString(object["id"])
	if id == "" {
		id = "call_" + strconv.Itoa(index)
	}

	if fnValue, ok := object["function"]; ok {
		function, ok := fnValue.(map[string]any)
		if !ok {
			return llms.ToolCall{}, false
		}

		name := asString(function["name"])
		if name == "" {
			return llms.ToolCall{}, false
		}

		arguments, err := marshalArguments(function["arguments"])
		if err != nil {
			return llms.ToolCall{}, false
		}

		return llms.ToolCall{
			ID:   id,
			Type: nonEmptyString(asString(object["type"]), "function"),
			FunctionCall: &llms.FunctionCall{
				Name:      name,
				Arguments: arguments,
			},
		}, true
	}

	name := firstString(object, "name", "tool")
	if name == "" {
		return llms.ToolCall{}, false
	}

	arguments, err := marshalArguments(object["arguments"])
	if err != nil {
		return llms.ToolCall{}, false
	}

	return llms.ToolCall{
		ID:   id,
		Type: nonEmptyString(asString(object["type"]), "function"),
		FunctionCall: &llms.FunctionCall{
			Name:      name,
			Arguments: arguments,
		},
	}, true
}

func parseFunctionCall(value any, defaultID string) (llms.ToolCall, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return llms.ToolCall{}, false
	}

	name := asString(object["name"])
	if name == "" {
		return llms.ToolCall{}, false
	}

	arguments, err := marshalArguments(object["arguments"])
	if err != nil {
		return llms.ToolCall{}, false
	}

	return llms.ToolCall{
		ID:   defaultID,
		Type: "function",
		FunctionCall: &llms.FunctionCall{
			Name:      name,
			Arguments: arguments,
		},
	}, true
}

func marshalArguments(value any) (string, error) {
	if value == nil {
		return "{}", nil
	}

	switch typed := value.(type) {
	case string:
		raw := strings.TrimSpace(typed)
		if raw == "" {
			return "{}", nil
		}
		if _, err := normalizeJSONObject(raw); err == nil {
			return raw, nil
		}
		wrapped, err := json.Marshal(map[string]any{"input": typed})
		if err != nil {
			return "", err
		}
		return string(wrapped), nil
	default:
		raw, err := json.Marshal(typed)
		if err != nil {
			return "", err
		}
		return string(raw), nil
	}
}

func normalizeJSONObject(raw string) (map[string]any, error) {
	var object map[string]any
	if err := json.Unmarshal([]byte(raw), &object); err != nil {
		return nil, err
	}
	return object, nil
}

func trimMarkdownCodeFence(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}

	lines := strings.Split(trimmed, "\n")
	if len(lines) < 2 {
		return trimmed
	}

	if strings.HasPrefix(lines[0], "```") {
		lines = lines[1:]
	}
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "```" {
		lines = lines[:len(lines)-1]
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func firstString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key].(string); ok {
			value = strings.TrimSpace(value)
			if value != "" {
				return value
			}
		}
	}
	return ""
}

func asString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func nonEmptyString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
