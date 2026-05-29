package nodes

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"weaveflow/core"
	"weaveflow/dsl"
	"weaveflow/llms/parts"
	"weaveflow/tools"

	fruntime "weaveflow/runtime"
	wfstate "weaveflow/state"

	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
)

type LLMNode struct {
	NodeInfo
	ToolIDs        []string
	StateScope     string
	PromptMaxChars int
	ToolContract   bool
}

const plannerToolResultSynthesisPrompt = `Planner step execution rule:
You just received tool result(s) for the current plan step.
Do not call more tools in this turn. Produce a concise step result for the current step.
Address the current step acceptance criteria directly.
If the evidence is insufficient, summarize exactly what was found and what is still missing instead of calling tools.
For broad research steps, prefer a useful partial result with explicit gaps over another tool call.`

const toolContractPromptHeader = `Tool usage contract:
- Call tools ONLY via the structured tool_use API. Never emit "<tool_call>...</tool_call>" or any XML/text-style tool calls inside your message content; such text is ignored and wastes the turn.
- Use the exact tool names listed below; do not invent names or aliases.
- Use the exact parameter names declared in the tool schema; do not rename them.
- If a step's required path or query is not satisfiable by any registered tool, return a textual gap report instead of fabricating a call.
Available tools:`

func NewLLMNode() *LLMNode {
	id := uuid.New()
	return &LLMNode{
		NodeInfo: NodeInfo{
			NodeID:          "LLM_" + id.String(),
			NodeName:        "LLM",
			NodeDescription: "LLM",
		},
	}
}

func (L *LLMNode) execute(ctx context.Context, state wfstate.State) (wfstate.State, error) {
	svc := core.ServicesFrom(ctx)
	if svc == nil || svc.Model == nil {
		return state, errors.New("llm node: model service not available")
	}
	model := svc.Model
	nodeTools := svc.FilterTools(L.ToolIDs)

	conversation := state.Conversation(L.StateScope)
	scrubPriorStepIfBoundaryCrossed(state, conversation)
	messages := conversation.Messages()
	promptMessages := trimLLMPromptMessages(messages, L.effectivePromptMaxChars())
	synthesizePlannerToolResult := shouldSynthesizePlannerToolResult(state, promptMessages)
	if synthesizePlannerToolResult {
		promptMessages = insertAfterLeadingSystem(
			promptMessages,
			llms.TextParts(llms.ChatMessageTypeSystem, plannerToolResultSynthesisPrompt),
		)
	} else if contract := buildToolContractSystemMessage(nodeTools); contract != nil {
		promptMessages = insertAfterLeadingSystem(promptMessages, *contract)
	}

	if conversation.IterationCount() >= conversation.MaxIterations() {
		message := "Maximum tool iterations reached. Please simplify the question or reduce tool usage."
		finalMessage := llms.TextParts(
			llms.ChatMessageTypeAI,
			message,
		)
		conversation.UpdateMessage(append(messages, finalMessage))
		conversation.SetFinalAnswer(message)

		return state, nil
	}

	var toolSets []llms.Tool
	if !synthesizePlannerToolResult {
		for _, tool := range nodeTools {
			toolSets = append(toolSets, tool.NewTool())
		}
	}
	if payload, err := buildLLMPromptArtifact(promptMessages, toolSets, L.StateScope, conversation.IterationCount(), conversation.MaxIterations()); err == nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "llm.prompt", payload)
	}

	resp, err := model.GenerateContent(
		ctx,
		promptMessages,
		llms.WithTools(toolSets),
		llms.WithThinkingMode(llms.ThinkingModeHigh),
	)
	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "llm.error", map[string]any{"error": err.Error()})
		return state, err
	}
	if resp == nil || len(resp.Choices) == 0 {
		err := errors.New("llm returned no choices")
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "llm.error", map[string]any{"error": err.Error()})
		return state, err
	}
	if payload := buildLLMResponseArtifact(resp); len(payload.Choices) > 0 {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "llm.response", payload)
	}

	choice := resp.Choices[0]
	_ = RecordChoiceUsage(ctx, state, Record{
		NodeID:     L.ID(),
		Model:      modelLabel(model),
		StateScope: L.StateScope,
	}, choice)

	aiMessage := llms.MessageContent{Role: llms.ChatMessageTypeAI}

	// Some LLMs require including the reasoning content in messages on the
	// next turn. Use parts.ReasoningPart so downstream consumers (openai
	// wrapper, state serializer, final-answer extractor) can route it
	// distinctly from regular assistant content.
	if strings.TrimSpace(choice.ReasoningContent) != "" {
		aiMessage.Parts = append(aiMessage.Parts, parts.NewReasoningPart(choice.ReasoningContent))
	}

	if strings.TrimSpace(choice.Content) != "" {
		aiMessage.Parts = append(aiMessage.Parts, llms.TextPart(choice.Content))
	}
	for _, toolCall := range choice.ToolCalls {
		if toolCall.Type == "" {
			_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventWarning, map[string]any{
				"message": "llm node received a tool call with no type",
			})
			continue
		}
		aiMessage.Parts = append(aiMessage.Parts, toolCall)
	}

	conversation.UpdateMessage(append(messages, aiMessage))
	conversation.IncrementIteration()

	if len(choice.ToolCalls) == 0 {
		conversation.SetFinalAnswer(extractText(aiMessage))
	}

	return state, nil
}

func (L *LLMNode) Execute(ctx context.Context, input wfstate.State) (wfstate.StatePatch, error) {
	return executeStatePatch(input, func(state wfstate.State) (wfstate.State, error) {
		return L.execute(ctx, state)
	})
}

func (L *LLMNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return dsl.GraphNodeSpec{
		ID:          L.ID(),
		Name:        L.Name(),
		Type:        "llm",
		Description: L.Description(),
		Config: map[string]any{
			"tool_ids":         append([]string(nil), L.ToolIDs...),
			"state_scope":      L.StateScope,
			"prompt_max_chars": L.effectivePromptMaxChars(),
		},
	}
}

func (L *LLMNode) effectivePromptMaxChars() int {
	if L == nil || L.PromptMaxChars <= 0 {
		return 20000
	}
	return L.PromptMaxChars
}

func scrubPriorStepIfBoundaryCrossed(state wfstate.State, conversation wfstate.ConversationFacet) bool {
	if state == nil || conversation == nil {
		return false
	}
	exec := state.Ensure(wfstate.StateKeyExecution)
	// IMPORTANT: do not type-assert exec["current_step"].(map[string]any)
	// directly — after a patch diff/apply round-trip the value may be the
	// named type wfstate.State, which is *not* identical to map[string]any
	// for the purposes of Go's type assertion. Use stateObjectAtPath, which
	// accepts both shapes and normalizes them.
	currentStep := stateObjectAtPath(state, wfstate.StateKeyExecution+".current_step")
	currentStepID := ""
	if currentStep != nil {
		currentStepID, _ = currentStep["id"].(string)
	}
	lastStepID, _ := exec["last_llm_step_id"].(string)
	if currentStepID == "" {
		return false
	}
	if lastStepID == "" {
		exec["last_llm_step_id"] = currentStepID
		return false
	}
	if currentStepID == lastStepID {
		return false
	}
	original := conversation.Messages()
	scrubbed := scrubAIAndToolMessages(original)
	if len(scrubbed) == len(original) {
		exec["last_llm_step_id"] = currentStepID
		return false
	}
	conversation.UpdateMessage(scrubbed)
	conversation.ResetIteration()
	exec["last_llm_step_id"] = currentStepID
	return true
}

func scrubAIAndToolMessages(messages []llms.MessageContent) []llms.MessageContent {
	if len(messages) == 0 {
		return messages
	}
	out := make([]llms.MessageContent, 0, len(messages))
	for _, msg := range messages {
		switch msg.Role {
		case llms.ChatMessageTypeSystem, llms.ChatMessageTypeHuman:
			out = append(out, msg)
		default:
			// drop AI replies, ToolCall, ToolCallResponse from the prior step
		}
	}
	return out
}

func buildToolContractSystemMessage(nodeTools map[string]tools.Tool) *llms.MessageContent {
	if len(nodeTools) == 0 {
		return nil
	}

	names := make([]string, 0, len(nodeTools))
	for name := range nodeTools {
		if strings.TrimSpace(name) == "" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	var lines []string
	lines = append(lines, toolContractPromptHeader)
	for _, name := range names {
		tool := nodeTools[name]
		if tool.Function == nil {
			continue
		}
		toolName := strings.TrimSpace(tool.Function.Name)
		if toolName == "" {
			toolName = name
		}
		required := extractRequiredParamNames(tool.Function.Parameters)
		desc := strings.TrimSpace(firstLine(tool.Function.Description))
		entry := "- " + toolName + "(" + strings.Join(required, ", ") + ")"
		if desc != "" {
			entry += " — " + desc
		}
		lines = append(lines, entry)
	}
	if len(lines) == 1 {
		return nil
	}
	message := llms.TextParts(llms.ChatMessageTypeSystem, strings.Join(lines, "\n"))
	return &message
}

func extractRequiredParamNames(parameters any) []string {
	schema, ok := parameters.(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := schema["required"]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		names := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				names = append(names, s)
			}
		}
		return names
	default:
		return nil
	}
}

func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if idx := strings.IndexAny(text, "\r\n"); idx >= 0 {
		return strings.TrimSpace(text[:idx])
	}
	return text
}

func extractText(message llms.MessageContent) string {
	parts := make([]string, 0, len(message.Parts))
	for _, part := range message.Parts {
		if textPart, ok := part.(llms.TextContent); ok {
			text := strings.TrimSpace(textPart.Text)
			if text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func shouldSynthesizePlannerToolResult(state wfstate.State, messages []llms.MessageContent) bool {
	if state == nil || len(messages) == 0 {
		return false
	}

	orchestration := state.Get(wfstate.StateKeyOrchestration)
	mode, _ := orchestration["mode"].(string)
	if !strings.EqualFold(strings.TrimSpace(mode), "planner") {
		return false
	}

	exec := state.Get(wfstate.StateKeyExecution)
	route, _ := exec["route"].(string)
	switch strings.ToLower(strings.TrimSpace(route)) {
	case ExecutionRouteLLM, ExecutionRouteLLMWithTools:
	default:
		return false
	}

	return messageHasToolResponses(messages[len(messages)-1])
}

func messageHasToolResponses(message llms.MessageContent) bool {
	for _, part := range message.Parts {
		if _, ok := part.(llms.ToolCallResponse); ok {
			return true
		}
	}
	return false
}

type llmPromptArtifact struct {
	StateScope     string                 `json:"state_scope,omitempty"`
	IterationCount int                    `json:"iteration_count,omitempty"`
	MaxIterations  int                    `json:"max_iterations,omitempty"`
	Messages       []wfstate.StateMessage `json:"messages,omitempty"`
	Tools          []llmToolArtifact      `json:"tools,omitempty"`
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
	serializedMessages, err := wfstate.SerializeMessages(messages)
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
		if usage := ExtractUsage(choice); !usage.IsZero() {
			item.Usage = usage.Artifact()
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

func trimLLMPromptMessages(messages []llms.MessageContent, maxChars int) []llms.MessageContent {
	if len(messages) == 0 || maxChars <= 0 {
		return messages
	}
	if promptMessagesCharCount(messages) <= maxChars {
		return messages
	}

	leadingSystem, body := splitLeadingSystemMessages(messages)
	prefix := cloneReducerMessages(leadingSystem)
	if promptMessagesCharCount(prefix) > maxChars && len(prefix) > 1 {
		prefix = prefix[:1]
	}

	// Pin the first human turn to the prefix so the original task survives
	// tail-keep trimming. Without this, a ReAct loop whose tool results exceed
	// maxChars loses the user's question across iterations and starts looping
	// on the same exploration steps.
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

	prefixCost := promptMessagesCharCount(prefix)
	if len(body) == 0 {
		return prefix
	}

	used := prefixCost
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
		if adjusted := adjustReducerTailStart(body, start); promptMessagesCharCount(append(cloneReducerMessages(prefix), body[adjusted:]...)) <= maxChars {
			start = adjusted
		}
	}
	if start >= len(body) {
		start = adjustReducerTailStart(body, len(body)-1)
	}

	result := make([]llms.MessageContent, 0, len(prefix)+len(body[start:]))
	result = append(result, prefix...)
	result = append(result, cloneReducerMessages(body[start:])...)
	return result
}

func splitLeadingSystemMessages(messages []llms.MessageContent) ([]llms.MessageContent, []llms.MessageContent) {
	index := 0
	for index < len(messages) && messages[index].Role == llms.ChatMessageTypeSystem {
		index++
	}
	return cloneReducerMessages(messages[:index]), cloneReducerMessages(messages[index:])
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
