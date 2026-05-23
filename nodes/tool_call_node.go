package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"weaveflow/core"
	"weaveflow/dsl"
	fruntime "weaveflow/runtime"
	wfstate "weaveflow/state"
	"weaveflow/tools"

	"github.com/google/uuid"

	"github.com/tmc/langchaingo/llms"
)

type ToolsNode struct {
	NodeInfo
	ToolIDs    []string
	StateScope string
	Parallel   bool
}

func NewToolCallNode() *ToolsNode {
	id := uuid.New()
	return &ToolsNode{
		NodeInfo: NodeInfo{
			NodeID:          "ToolCall_" + id.String(),
			NodeName:        "ToolCall",
			NodeDescription: "Execute tool calls emitted by the model.",
		},
		Parallel: true,
	}
}

func (t *ToolsNode) execute(ctx context.Context, state wfstate.State) (wfstate.State, error) {
	svc := core.ServicesFrom(ctx)
	nodeTools := svc.FilterTools(t.ToolIDs)

	conversation := state.Conversation(t.StateScope)

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
		t.publishToolCallsStart(ctx, toolCalls, true)
		wg.Add(len(toolCalls))
		for index, toolCall := range toolCalls {
			go func(index int, toolCall llms.ToolCall) {
				defer wg.Done()
				toolMessages[index] = executeToolCallMessage(ctx, nodeTools, toolCall)
			}(index, toolCall)
		}
		wg.Wait()
	} else {
		for index, toolCall := range toolCalls {
			t.publishToolCallStart(ctx, toolCall)
			toolMessages[index] = executeToolCallMessage(ctx, nodeTools, toolCall)
		}
	}

	conversation.UpdateMessage(append(messages, toolMessages...))

	return state, nil
}

func (t *ToolsNode) Execute(ctx context.Context, input wfstate.State) (wfstate.StatePatch, error) {
	return executeStatePatch(input, func(state wfstate.State) (wfstate.State, error) {
		return t.execute(ctx, state)
	})
}

func (t *ToolsNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return dsl.GraphNodeSpec{
		ID:          t.ID(),
		Name:        t.Name(),
		Type:        "tools",
		Description: t.Description(),
		Config: map[string]any{
			"tool_ids":    append([]string(nil), t.ToolIDs...),
			"state_scope": t.StateScope,
		},
	}
}

func executeToolCall(ctx context.Context, available map[string]tools.Tool, toolCall llms.ToolCall) (string, error) {
	if toolCall.FunctionCall == nil {
		return "", errors.New("tool call has no function payload")
	}

	tool, ok := findAvailableTool(available, toolCall.FunctionCall.Name)
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
	t.publishToolCallsStart(ctx, []llms.ToolCall{toolCall}, false)
}

func (t *ToolsNode) publishToolCallsStart(ctx context.Context, toolCalls []llms.ToolCall, parallel bool) {
	if len(toolCalls) == 0 {
		return
	}
	if len(toolCalls) == 1 {
		publishSingleToolCallStart(ctx, toolCalls[0])
		return
	}

	items := make([]toolCallEventItem, 0, len(toolCalls))
	artifactItems := make([]map[string]any, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		name := toolCallName(toolCall)
		arguments := toolCallArguments(toolCall)
		items = append(items, toolCallEventItem{
			ToolCallID: toolCall.ID,
			Name:       name,
			Arguments:  arguments,
		})
		artifactItems = append(artifactItems, map[string]any{
			"tool_call_id": toolCall.ID,
			"name":         name,
			"arguments":    arguments,
			"input":        decodeToolInput(arguments),
		})
	}

	payload := toolCallBatchEventPayload{
		Tools:    items,
		Count:    len(items),
		Parallel: parallel,
	}
	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventToolCalled, payload)
	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "tool.inputs", map[string]any{
		"tools":    artifactItems,
		"count":    len(artifactItems),
		"parallel": parallel,
	})
}

func publishSingleToolCallStart(ctx context.Context, toolCall llms.ToolCall) {
	name := toolCallName(toolCall)
	arguments := toolCallArguments(toolCall)

	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventToolCalled, map[string]any{
		"tool_call_id": toolCall.ID,
		"name":         name,
		"arguments":    arguments,
	})
	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "tool.input", map[string]any{
		"tool_call_id": toolCall.ID,
		"name":         name,
		"arguments":    arguments,
		"input":        decodeToolInput(arguments),
	})
}

type toolCallEventItem struct {
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
	Arguments  string `json:"arguments,omitempty"`
	Status     string `json:"status,omitempty"`
	Content    string `json:"content,omitempty"`
	Error      string `json:"error,omitempty"`
}

type toolCallBatchEventPayload struct {
	Tools    []toolCallEventItem `json:"tools"`
	Count    int                 `json:"count"`
	Parallel bool                `json:"parallel,omitempty"`
}

type toolCallExecutionResult struct {
	Message llms.MessageContent
	Event   toolCallEventItem
	Err     error
}

func executeToolCallMessage(ctx context.Context, available map[string]tools.Tool, toolCall llms.ToolCall) llms.MessageContent {
	result := executeToolCallResult(ctx, available, toolCall)
	publishSingleToolCallExecutionResult(ctx, result)
	return result.Message
}

func executeToolCallResult(ctx context.Context, available map[string]tools.Tool, toolCall llms.ToolCall) toolCallExecutionResult {
	name := toolCallName(toolCall)
	result, err := executeToolCall(ctx, available, toolCall)
	eventItem := toolCallEventItem{
		ToolCallID: toolCall.ID,
		Name:       name,
		Arguments:  toolCallArguments(toolCall),
	}
	if err != nil {
		eventItem.Status = "failed"
		eventItem.Error = err.Error()
		result = "tool execution failed: " + err.Error()
	} else {
		eventItem.Status = "succeeded"
		eventItem.Content = result
	}

	message := llms.MessageContent{
		Role: llms.ChatMessageTypeTool,
		Parts: []llms.ContentPart{
			llms.ToolCallResponse{
				ToolCallID: toolCall.ID,
				Name:       name,
				Content:    result,
			},
		},
	}

	return toolCallExecutionResult{
		Message: message,
		Event:   eventItem,
		Err:     err,
	}
}

func publishSingleToolCallExecutionResult(ctx context.Context, result toolCallExecutionResult) {
	payload := map[string]any{
		"tool_call_id": result.Event.ToolCallID,
		"name":         result.Event.Name,
	}
	if result.Event.Arguments != "" {
		payload["arguments"] = result.Event.Arguments
	}
	if result.Err != nil {
		payload["error"] = result.Event.Error
		_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventToolFailed, payload)
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "tool.output", payload)
		return
	}

	payload["content"] = result.Event.Content
	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventToolReturned, payload)
	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "tool.output", payload)
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

	if len(payload) == 1 {
		if input, ok := payload["input"].(string); ok {
			return input
		}
		if expression, ok := payload["expression"].(string); ok {
			return expression
		}
	}

	return raw
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

func findAvailableTool(available map[string]tools.Tool, name string) (tools.Tool, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return tools.Tool{}, false
	}

	if tool, ok := available[name]; ok {
		return tool, true
	}

	for key, tool := range available {
		if strings.EqualFold(strings.TrimSpace(key), name) {
			return tool, true
		}
		if strings.EqualFold(strings.TrimSpace(tool.Name()), name) {
			return tool, true
		}
	}

	return tools.Tool{}, false
}
