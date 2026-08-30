package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/llms"
)

func webSearchTool(ctx context.Context, call llms.ToolCall) (llms.ToolResult, error) {
	var request struct {
		Query string `json:"query"`
	}
	if err := decodeToolArguments(call, &request); err != nil {
		return llms.ToolResult{}, fmt.Errorf("web_search input: %w", err)
	}
	request.Query = strings.TrimSpace(request.Query)
	if request.Query == "" {
		return llms.ToolResult{}, fmt.Errorf("query is required")
	}
	tavilyKey := core.EnvironmentVariableFromContext(ctx, tavilyAPIKeyEnvironment)
	braveKey := core.EnvironmentVariableFromContext(ctx, braveAPIKeyEnvironment)
	results, err := newWebSearcher(tavilyKey, braveKey).Search(ctx, request.Query, defaultSearchResultLimit)
	if err != nil {
		return llms.ToolResult{}, fmt.Errorf("web_search failed: %w", err)
	}
	return textToolResult(call, formatSearchResults(results)), nil
}

func formatSearchResults(results []searchResult) string {
	if len(results) == 0 {
		return "No search results found."
	}
	var output strings.Builder
	for _, result := range results {
		_, _ = fmt.Fprintf(&output, "Source: %s\nTitle: %s\nURL: %s\nContent: %s\n\n", result.Engine, result.Title, result.URL, result.Snippet)
	}
	return output.String()
}

func NewWebSearch() Tool {
	return Tool{
		Function: &llms.FunctionDefinition{
			Name: "web_search",
			Description: "- Allows the assistant to search the web and use the results to inform responses\n" +
				"- Provides up-to-date information for current events and recent data\n" +
				"- Returns search result information including links and snippets\n" +
				"- Use this tool for accessing information beyond model knowledge",
			OutputSchema: textOutputSchema(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"minLength":   2,
						"description": "The search query to use",
					},
				},
				"required":             []string{"query"},
				"additionalProperties": false,
			},
		},
		Handler:     webSearchTool,
		Effect:      EffectReadOnly,
		Permissions: []string{"network.search"},
	}
}
