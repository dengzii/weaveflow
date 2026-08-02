package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/tmc/langchaingo/llms"
)

func NewCurrentTime() Tool {
	return Tool{
		Function: &llms.FunctionDefinition{
			Name:        "current_time",
			Description: "Return the current local time and UTC time. Use this when the user asks for the current time or date.",
			Parameters: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
			},
		},
		Handler: currentTimeTool,
	}
}

func currentTimeTool(_ context.Context, input string) (string, error) {
	var req struct{}
	if err := decodeToolRequest(input, "current_time", &req); err != nil {
		return "", err
	}
	now := time.Now()
	return fmt.Sprintf(
		"local=%s; utc=%s",
		now.Format(time.RFC3339),
		now.UTC().Format(time.RFC3339),
	), nil
}
