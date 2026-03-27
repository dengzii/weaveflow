package falcon

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
)

type LLMNode[S BaseState] struct {
	NodeInfo
	model      llms.Model
	tools      map[string]Tool
	StateScope string
}

func NewLLMNode[S BaseState](model llms.Model, tools map[string]Tool) *LLMNode[S] {
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
		tools: cloneTools(tools),
	}
}

func (L *LLMNode[S]) Invoke(ctx context.Context, state S) (S, error) {
	stateView := scopedState(state, L.StateScope)

	if stateView.IterationCount() >= stateView.MaxIterations() {
		message := "Maximum tool iterations reached. Please simplify the question or reduce tool usage."
		finalMessage := llms.TextParts(
			llms.ChatMessageTypeAI,
			message,
		)
		stateView.UpdateMessage(append(stateView.GetMessages(), finalMessage))
		stateView.SetFinalAnswer(message)

		return state, nil
	}

	var tools []llms.Tool
	for _, tool := range L.tools {
		tools = append(tools, tool.LLMTool())
	}

	resp, err := L.model.GenerateContent(
		ctx,
		stateView.GetMessages(),
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

	stateView.UpdateMessage(append(stateView.GetMessages(), aiMessage))
	stateView.IncrementIteration()

	if len(choice.ToolCalls) == 0 {
		stateView.SetFinalAnswer(extractText(aiMessage))
	}

	return state, nil
}

func (L *LLMNode[S]) GraphNodeSpec() GraphNodeSpec {
	toolIDs := make([]string, 0, len(L.tools))
	for id := range L.tools {
		toolIDs = append(toolIDs, id)
	}
	sort.Strings(toolIDs)

	return GraphNodeSpec{
		ID:          L.ID(),
		Name:        L.Name(),
		Type:        "llm",
		Description: L.Description(),
		Config: map[string]any{
			"tool_ids":    toolIDs,
			"state_scope": L.StateScope,
		},
	}
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
