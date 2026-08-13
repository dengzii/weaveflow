// Package tools provides bundled tool implementations for agent workflows.
package tools

import (
	"encoding/json"
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/llms"
	"github.com/dengzii/weaveflow/state"
)

type Tool = core.Tool
type ToolHandler = core.ToolHandler

func decodeToolArguments(call llms.ToolCall, target any) error {
	return core.DecodeToolArguments(call, target)
}

func toolCallName(call llms.ToolCall) string {
	if call.FunctionCall == nil {
		return ""
	}
	return strings.TrimSpace(call.FunctionCall.Name)
}

func textToolResult(call llms.ToolCall, content string) llms.ToolResult {
	return llms.ToolResult{
		ToolCallID: call.ID,
		Name:       toolCallName(call),
		Content:    content,
		Value:      content,
	}
}

func structuredToolResult(call llms.ToolCall, value any) (llms.ToolResult, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return llms.ToolResult{}, err
	}
	return structuredToolResultWithContent(call, value, string(data)), nil
}

func structuredToolResultWithContent(call llms.ToolCall, value any, content string) llms.ToolResult {
	return llms.ToolResult{
		ToolCallID: call.ID,
		Name:       toolCallName(call),
		Content:    content,
		Value:      value,
	}
}

func textOutputSchema() state.JSONSchema {
	return state.JSONSchema{"type": "string"}
}

func objectOutputSchema(properties map[string]any, required ...string) state.JSONSchema {
	schema := state.JSONSchema{
		"type":                 "object",
		"properties":           state.JSONSchema(properties),
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = append([]string(nil), required...)
	}
	return schema
}
