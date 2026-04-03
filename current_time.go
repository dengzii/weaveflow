package falcon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tmc/langchaingo/llms"
)

func NewCurrentTime() Tool {
	return Tool{
		Function: &llms.FunctionDefinition{
			Name:        "current_time",
			Description: "Return the current local time and UTC time. Use this when the user asks for the current time or date.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input": map[string]any{
						"type":        "string",
						"description": "Optional note to include with the response.",
					},
				},
				"required":             []string{"input"},
				"additionalProperties": false,
			},
		},
		Handler: currentTimeTool,
	}
}

func currentTimeTool(_ context.Context, input string) (string, error) {
	now := time.Now()
	return fmt.Sprintf(
		"local=%s; utc=%s; note=%s",
		now.Format(time.RFC3339),
		now.UTC().Format(time.RFC3339),
		strings.TrimSpace(input),
	), nil
}
