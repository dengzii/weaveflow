package falcon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/tmc/langchaingo/llms"
)

type ToolHandler func(ctx context.Context, input string) (string, error)

type Tool struct {
	Function *llms.FunctionDefinition
	Handler  ToolHandler
}

func (t Tool) Name() string {
	if t.Function == nil {
		return ""
	}
	return t.Function.Name
}

func (t Tool) LLMTool() llms.Tool {
	return llms.Tool{
		Type:     "function",
		Function: cloneFunctionDefinition(t.Function),
	}
}

type ToolsNode[S BaseState] struct {
	NodeInfo
	Tools      map[string]Tool
	StateScope string
}

func NewToolsNode[S BaseState](tools map[string]Tool) *ToolsNode[S] {
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
		Tools: cloneTools(tools),
	}
}

func (t *ToolsNode[S]) Invoke(ctx context.Context, state S) (S, error) {
	stateView := scopedState(state, t.StateScope)

	if len(stateView.GetMessages()) == 0 {
		return state, errors.New("no messages available for tool execution")
	}

	lastMessage := stateView.GetMessages()[len(stateView.GetMessages())-1]
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

	stateView.UpdateMessage(append(stateView.GetMessages(), toolMessages...))

	return state, nil
}

func (t *ToolsNode[S]) GraphNodeSpec() GraphNodeSpec {
	toolIDs := make([]string, 0, len(t.Tools))
	for id := range t.Tools {
		toolIDs = append(toolIDs, id)
	}
	sort.Strings(toolIDs)

	return GraphNodeSpec{
		ID:          t.ID(),
		Name:        t.Name(),
		Type:        "tools",
		Description: t.Description(),
		Config: map[string]any{
			"tool_ids":    toolIDs,
			"state_scope": t.StateScope,
		},
	}
}

func (t *ToolsNode[S]) executeToolCall(ctx context.Context, toolCall llms.ToolCall) (string, error) {
	if toolCall.FunctionCall == nil {
		return "", errors.New("tool call has no function payload")
	}

	tool, ok := t.Tools[toolCall.FunctionCall.Name]
	if !ok {
		return "", fmt.Errorf("tool %q not found", toolCall.FunctionCall.Name)
	}
	if tool.Function == nil {
		return "", fmt.Errorf("tool %q has no function definition", toolCall.FunctionCall.Name)
	}
	if tool.Handler == nil {
		return "", fmt.Errorf("tool handler %q not found", tool.Function.Name)
	}

	input := decodeToolInput(toolCall.FunctionCall.Arguments)
	return tool.Handler(ctx, input)
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

func cloneTools(all map[string]Tool) map[string]Tool {
	if len(all) == 0 {
		return nil
	}
	cloned := make(map[string]Tool, len(all))
	for key, value := range all {
		cloned[key] = Tool{
			Function: cloneFunctionDefinition(value.Function),
			Handler:  value.Handler,
		}
	}
	return cloned
}

func cloneFunctionDefinition(function *llms.FunctionDefinition) *llms.FunctionDefinition {
	if function == nil {
		return nil
	}
	cloned := *function
	return &cloned
}
