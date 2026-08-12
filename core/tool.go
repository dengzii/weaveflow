package core

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

type ToolHandler func(ctx context.Context, input string) (string, error)

type ToolExecutionMode string

const (
	ToolExecutionLeaf      ToolExecutionMode = "leaf"
	ToolExecutionComposite ToolExecutionMode = "composite"
)

type Tool struct {
	Function      *llms.FunctionDefinition
	Handler       ToolHandler
	ExecutionMode ToolExecutionMode
}

func NewTool(function *llms.FunctionDefinition, handler ToolHandler) Tool {
	return Tool{
		Function: function,
		Handler:  handler,
	}
}

func (t Tool) Name() string {
	if t.Function == nil {
		return ""
	}
	return t.Function.Name
}

func (t Tool) NewTool() llms.Tool {
	return llms.Tool{
		Type:     "function",
		Function: cloneFunctionDefinition(t.Function),
	}
}

type ToolCallMetadata struct {
	ToolCallID string
	Name       string
	Arguments  string
}

type toolCallMetadataKey struct{}

func WithToolCallMetadata(ctx context.Context, metadata ToolCallMetadata) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, toolCallMetadataKey{}, metadata)
}

func ToolCallMetadataFromContext(ctx context.Context) (ToolCallMetadata, bool) {
	if ctx == nil {
		return ToolCallMetadata{}, false
	}
	metadata, ok := ctx.Value(toolCallMetadataKey{}).(ToolCallMetadata)
	return metadata, ok
}

func DecodeToolInput(arguments string) string {
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

func FindTool(available map[string]Tool, name string) (Tool, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Tool{}, false
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
	return Tool{}, false
}

func cloneFunctionDefinition(function *llms.FunctionDefinition) *llms.FunctionDefinition {
	if function == nil {
		return nil
	}
	cloned := *function
	return &cloned
}
