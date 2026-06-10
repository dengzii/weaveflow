package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"weaveflow/core"
	"weaveflow/tools"
)

type ToolCallRequest struct {
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
	Arguments  string `json:"arguments,omitempty"`
}

type toolCallExecutorFunc func(ctx context.Context, req ToolCallRequest) (string, error)

type toolCallExecutorKey struct{}

func withToolCallExecutor(ctx context.Context, executor toolCallExecutorFunc) context.Context {
	if executor == nil {
		return ctx
	}
	return context.WithValue(ctx, toolCallExecutorKey{}, executor)
}

func ExecuteToolCall(ctx context.Context, req ToolCallRequest) (string, error) {
	executor, _ := ctx.Value(toolCallExecutorKey{}).(toolCallExecutorFunc)
	if executor != nil {
		return executor(ctx, req)
	}
	return executeToolCallDirect(ctx, req)
}

func newToolCallExecutor(runner *GraphRunner, runID, stepID, nodeID string) toolCallExecutorFunc {
	return func(ctx context.Context, req ToolCallRequest) (string, error) {
		_ = runner.publishEvent(ctx, RunRecord{RunID: runID}, stepID, nodeID, EventToolCalled, map[string]any{
			"tool_call_id": req.ToolCallID,
			"name":         req.Name,
			"arguments":    req.Arguments,
		})

		input := decodeToolInput(req.Arguments)
		_, _ = SaveJSONArtifactBestEffort(ctx, "tool.input", map[string]any{
			"tool_call_id": req.ToolCallID,
			"name":         req.Name,
			"arguments":    req.Arguments,
			"input":        input,
		})

		result, err := executeToolCallDirect(ctx, req)

		if err != nil {
			_ = runner.publishEvent(ctx, RunRecord{RunID: runID}, stepID, nodeID, EventToolFailed, map[string]any{
				"tool_call_id": req.ToolCallID,
				"name":         req.Name,
				"error":        err.Error(),
			})
			_, _ = SaveJSONArtifactBestEffort(ctx, "tool.output", map[string]any{
				"tool_call_id": req.ToolCallID,
				"name":         req.Name,
				"error":        err.Error(),
			})
			return result, err
		}

		_ = runner.publishEvent(ctx, RunRecord{RunID: runID}, stepID, nodeID, EventToolReturned, map[string]any{
			"tool_call_id": req.ToolCallID,
			"name":         req.Name,
			"content":      result,
		})
		_, _ = SaveJSONArtifactBestEffort(ctx, "tool.output", map[string]any{
			"tool_call_id": req.ToolCallID,
			"name":         req.Name,
			"content":      result,
		})
		return result, nil
	}
}

func executeToolCallDirect(ctx context.Context, req ToolCallRequest) (string, error) {
	coreCtx := core.NewContext(ctx, nil)
	available := coreCtx.Tools()
	if available == nil {
		return "", fmt.Errorf("tool %q: tools not available", req.Name)
	}
	tool, ok := findAvailableTool(available, req.Name)
	if !ok {
		return "", fmt.Errorf("tool %q not found", req.Name)
	}
	if tool.Handler == nil {
		return "", fmt.Errorf("tool handler %q not found", req.Name)
	}
	input := decodeToolInput(req.Arguments)
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
