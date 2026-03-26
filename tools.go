package falcon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

type AgentTool interface {
	Name() string
	Description() string
	Call(ctx context.Context, input string) (string, error)
}

type ToolsNode[S BaseState] struct {
	NodeInfo
	Tools map[string]AgentTool
}

func NewToolsNode[S BaseState](tools map[string]AgentTool) *ToolsNode[S] {
	id, err := uuid.NewUUID()
	if err != nil {
		panic(err)
	}
	return &ToolsNode[S]{
		NodeInfo: NodeInfo{
			NodeID:          id.String(),
			NodeName:        "Tools Node",
			NodeDescription: "Tools Node",
		},
		Tools: tools,
	}
}

func (t *ToolsNode[S]) Invoke(ctx context.Context, state S) (S, error) {

	if len(state.GetMessages()) == 0 {
		return state, errors.New("no messages available for tool execution")
	}

	lastMessage := state.GetMessages()[len(state.GetMessages())-1]
	if lastMessage.Role != llms.ChatMessageTypeAI {
		return state, errors.New("last message is not an AI message")
	}

	toolMessages := make([]llms.MessageContent, 0, len(lastMessage.Parts))
	for _, part := range lastMessage.Parts {
		toolCall, ok := part.(llms.ToolCall)
		if !ok {
			continue
		}

		result, err := t.executeToolCall(ctx, toolCall)
		if err != nil {
			result = "tool execution failed: " + err.Error()
		}

		toolMessages = append(toolMessages, llms.MessageContent{
			Role: llms.ChatMessageTypeTool,
			Parts: []llms.ContentPart{
				llms.ToolCallResponse{
					ToolCallID: toolCall.ID,
					Name:       toolCall.FunctionCall.Name,
					Content:    result,
				},
			},
		})
	}

	state.UpdateMessage(append(state.GetMessages(), toolMessages...))

	return state, nil
}

func (t *ToolsNode[S]) executeToolCall(ctx context.Context, toolCall llms.ToolCall) (string, error) {
	if toolCall.FunctionCall == nil {
		return "", errors.New("tool call has no function payload")
	}

	tool, ok := t.Tools[toolCall.FunctionCall.Name]
	if !ok {
		return "", fmt.Errorf("tool %q not found", toolCall.FunctionCall.Name)
	}

	input := decodeToolInput(toolCall.FunctionCall.Arguments)
	return tool.Call(ctx, input)
}

func decodeToolInput(arguments string) string {
	raw := strings.TrimSpace(arguments)
	if raw == "" {
		return ""
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return raw
	}

	if input, ok := payload["input"].(string); ok {
		return input
	}
	if expression, ok := payload["expression"].(string); ok {
		return expression
	}

	for _, value := range payload {
		if text, ok := value.(string); ok {
			return text
		}
	}

	return raw
}
