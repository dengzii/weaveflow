package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/dengzii/weaveflow/llms"
)

func NewCurrentTime() Tool {
	return Tool{
		Function: &llms.FunctionDefinition{
			Name:         "current_time",
			Description:  "Return the current local time and UTC time. Use this when the user asks for the current time or date.",
			OutputSchema: textOutputSchema(),
			Parameters: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
			},
		},
		Handler: currentTimeTool,
	}
}

func currentTimeTool(_ context.Context, call llms.ToolCall) (llms.ToolResult, error) {
	var req struct{}
	if err := decodeToolArguments(call, &req); err != nil {
		return llms.ToolResult{}, fmt.Errorf("current_time input: %w", err)
	}
	now := time.Now()
	return textToolResult(call, fmt.Sprintf(
		"local=%s; utc=%s",
		now.Format(time.RFC3339),
		now.UTC().Format(time.RFC3339),
	)), nil
}
