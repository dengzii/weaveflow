package nodes

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"weaveflow/dsl"

	fruntime "weaveflow/runtime"

	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
)

type LLMNode struct {
	NodeInfo
	ToolIDs    []string
	StateScope string
}

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

func (L *LLMNode) Invoke(ctx context.Context, state fruntime.State) (fruntime.State, error) {
	svc := fruntime.ServicesFrom(ctx)
	if svc == nil || svc.Model == nil {
		return state, errors.New("llm node: model service not available")
	}
	model := svc.Model
	nodeTools := svc.FilterTools(L.ToolIDs)

	conversation := state.Conversation(L.StateScope)
	messages := conversation.Messages()

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
	for _, tool := range nodeTools {
		toolSets = append(toolSets, tool.NewTool())
	}
	if payload, err := buildLLMPromptArtifact(messages, toolSets, L.StateScope, conversation.IterationCount(), conversation.MaxIterations()); err == nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "llm.prompt", payload)
	}

	resp, err := model.GenerateContent(
		ctx,
		messages,
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
	usage := Extract(choice)
	record := RecordState(state, Record{
		NodeID:             L.ID(),
		Model:              modelLabel(model),
		StateScope:         L.StateScope,
		StopReason:         choice.StopReason,
		PromptTokens:       usage.PromptTokens,
		CompletionTokens:   usage.CompletionTokens,
		TotalTokens:        usage.TotalTokens,
		ReasoningTokens:    usage.ReasoningTokens,
		PromptCachedTokens: usage.PromptCachedTokens,
	})
	_ = PublishUsageEvent(ctx, record)

	aiMessage := llms.MessageContent{Role: llms.ChatMessageTypeAI}

	if strings.TrimSpace(choice.ReasoningContent) != "" {
		// reasoning content is necessary for some models, but not all.
		// aiMessage.Parts = append(aiMessage.Parts, llms.TextPart(choice.ReasoningContent))
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

func (L *LLMNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return dsl.GraphNodeSpec{
		ID:          L.ID(),
		Name:        L.Name(),
		Type:        "llm",
		Description: L.Description(),
		Config: map[string]any{
			"tool_ids":    append([]string(nil), L.ToolIDs...),
			"state_scope": L.StateScope,
		},
	}
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

type llmPromptArtifact struct {
	StateScope     string                  `json:"state_scope,omitempty"`
	IterationCount int                     `json:"iteration_count,omitempty"`
	MaxIterations  int                     `json:"max_iterations,omitempty"`
	Messages       []fruntime.StateMessage `json:"messages,omitempty"`
	Tools          []llmToolArtifact       `json:"tools,omitempty"`
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
	serializedMessages, err := fruntime.SerializeMessages(messages)
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
		if usage := Extract(choice); !usage.IsZero() {
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

func modelLabel(model llms.Model) string {
	if model == nil {
		return ""
	}
	if named, ok := model.(interface{ Name() string }); ok {
		if name := strings.TrimSpace(named.Name()); name != "" {
			return name
		}
	}
	typed := reflect.TypeOf(model)
	if typed == nil {
		return ""
	}
	return typed.String()
}
