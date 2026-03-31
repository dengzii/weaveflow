package falcon

import (
	"context"
	"encoding/json"
	"errors"
	fruntime "falcon/runtime"
	"fmt"
	"sort"
	"strings"
	"sync"

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

func (t Tool) ToolCallNode() llms.Tool {
	return llms.Tool{
		Type:     "function",
		Function: cloneFunctionDefinition(t.Function),
	}
}

type ToolsNode struct {
	NodeInfo
	Tools      map[string]Tool
	StateScope string
	Parallel   bool
}

func NewToolCallNode(tools map[string]Tool) *ToolsNode {
	id, err := uuid.NewUUID()
	if err != nil {
		panic(err)
	}
	return &ToolsNode{
		NodeInfo: NodeInfo{
			NodeID:          id.String(),
			NodeName:        "ToolCall",
			NodeDescription: "ToolCall",
		},
		Tools:    cloneTools(tools),
		Parallel: true,
	}
}

func (t *ToolsNode) Invoke(ctx context.Context, state State) (State, error) {
	conversation := fruntime.Conversation(state, t.StateScope)

	messages := conversation.Messages()
	if len(messages) == 0 {
		return state, errors.New("no messages available for tool execution")
	}

	lastMessage := messages[len(messages)-1]
	if lastMessage.Role != llms.ChatMessageTypeAI {
		return state, errors.New("last message is not an AI message")
	}

	toolCalls := make([]llms.ToolCall, 0, len(lastMessage.Parts))
	for _, part := range lastMessage.Parts {
		toolCall, ok := part.(llms.ToolCall)
		if !ok {
			continue
		}
		toolCalls = append(toolCalls, toolCall)
	}

	toolMessages := make([]llms.MessageContent, len(toolCalls))

	if t.Parallel {
		var wg sync.WaitGroup
		wg.Add(len(toolCalls))
		for index, toolCall := range toolCalls {
			t.publishToolCallStart(ctx, toolCall)

			go func(index int, toolCall llms.ToolCall) {
				defer wg.Done()
				toolMessages[index] = t.executeToolCallMessage(ctx, toolCall)
			}(index, toolCall)
		}
		wg.Wait()
	} else {
		for index, toolCall := range toolCalls {
			t.publishToolCallStart(ctx, toolCall)
			toolMessages[index] = t.executeToolCallMessage(ctx, toolCall)
		}
	}

	conversation.UpdateMessage(append(messages, toolMessages...))

	return state, nil
}

func (t *ToolsNode) GraphNodeSpec() GraphNodeSpec {
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

func (t *ToolsNode) executeToolCall(ctx context.Context, toolCall llms.ToolCall) (string, error) {
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

func (t *ToolsNode) publishToolCallStart(ctx context.Context, toolCall llms.ToolCall) {
	name := toolCallName(toolCall)
	arguments := toolCallArguments(toolCall)

	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventToolCalled, map[string]any{
		"tool_call_id": toolCall.ID,
		"name":         name,
	})
	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "tool.input", map[string]any{
		"tool_call_id": toolCall.ID,
		"name":         name,
		"arguments":    arguments,
		"input":        decodeToolInput(arguments),
	})
}

func (t *ToolsNode) executeToolCallMessage(ctx context.Context, toolCall llms.ToolCall) llms.MessageContent {
	name := toolCallName(toolCall)
	result, err := t.executeToolCall(ctx, toolCall)
	if err != nil {
		_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventToolFailed, map[string]any{
			"tool_call_id": toolCall.ID,
			"name":         name,
			"error":        err.Error(),
		})
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "tool.output", map[string]any{
			"tool_call_id": toolCall.ID,
			"name":         name,
			"error":        err.Error(),
		})
		result = "tool execution failed: " + err.Error()
	} else {
		_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventToolReturned, map[string]any{
			"tool_call_id": toolCall.ID,
			"name":         name,
			"content":      result,
		})
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "tool.output", map[string]any{
			"tool_call_id": toolCall.ID,
			"name":         name,
			"content":      result,
		})
	}

	return llms.MessageContent{
		Role: llms.ChatMessageTypeTool,
		Parts: []llms.ContentPart{
			llms.ToolCallResponse{
				ToolCallID: toolCall.ID,
				Name:       name,
				Content:    result,
			},
		},
	}
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

func toolCallName(toolCall llms.ToolCall) string {
	if toolCall.FunctionCall == nil {
		return ""
	}
	return toolCall.FunctionCall.Name
}

func toolCallArguments(toolCall llms.ToolCall) string {
	if toolCall.FunctionCall == nil {
		return ""
	}
	return toolCall.FunctionCall.Arguments
}
