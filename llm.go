package falcon

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"strings"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
)

type LLMNode[S BaseState] struct {
	NodeInfo
	model llms.Model
	tools map[string]llms.Tool
}

func NewLLMNode[S BaseState](model llms.Model, tools map[string]llms.Tool) *LLMNode[S] {
	clonedTools := make(map[string]llms.Tool, len(tools))
	for name, tool := range tools {
		clonedTools[name] = tool
	}

	id, err := uuid.NewUUID()
	if err != nil {
		panic(err)
	}

	return &LLMNode[S]{
		NodeInfo: NodeInfo{
			NodeID:          id.String(),
			NodeName:        "LLM Node",
			NodeDescription: "LLM Node",
		},
		model: model,
		tools: clonedTools,
	}
}

func (L *LLMNode[S]) Invoke(ctx context.Context, state S) (S, error) {
	if state.IterationCount() >= state.MaxIterations() {
		message := "Maximum tool iterations reached. Please simplify the question or reduce tool usage."
		finalMessage := llms.TextParts(
			llms.ChatMessageTypeAI,
			message,
		)
		state.UpdateMessage(append(state.GetMessages(), finalMessage))
		state.SetFinalAnswer(message)

		return state, nil
	}

	var tools []llms.Tool
	for _, id := range state.EnabledTools() {
		if tool, ok := L.tools[id]; ok {
			tools = append(tools, tool)
		}
	}

	resp, err := L.model.GenerateContent(
		ctx,
		state.GetMessages(),
		llms.WithTools(tools),
		llms.WithTemperature(0.1),
		llms.WithStreamingReasoningFunc(onStreamingResponse),
		openai.WithMaxCompletionTokens(10000),
	)
	if err != nil {
		return state, err
	}
	if resp == nil || len(resp.Choices) == 0 {
		return state, errors.New("llm returned no choices")
	}

	choice := resp.Choices[0]
	aiMessage := llms.MessageContent{Role: llms.ChatMessageTypeAI}
	if strings.TrimSpace(choice.Content) != "" {
		aiMessage.Parts = append(aiMessage.Parts, llms.TextPart(choice.Content))
	}
	for _, toolCall := range choice.ToolCalls {
		aiMessage.Parts = append(aiMessage.Parts, toolCall)
	}

	state.UpdateMessage(append(state.GetMessages(), aiMessage))
	state.IncrementIteration()

	if len(choice.ToolCalls) == 0 {
		state.SetFinalAnswer(extractText(aiMessage))
	}

	return state, nil
}

func onStreamingResponse(_ context.Context, reasoningChunk, chunk []byte) error {
	reasoning := string(reasoningChunk)
	if strings.TrimSpace(reasoning) != "" {
		fmt.Print(reasoning)
	}
	content := string(chunk)
	if strings.TrimSpace(content) != "" {
		fmt.Print(content)
	}
	return nil
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
