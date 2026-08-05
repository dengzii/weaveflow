package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/core"

	"github.com/smallnest/langgraphgo/tool"
	"github.com/tmc/langchaingo/llms"
)

const tavilyAPIKeyEnvironment = "TAVILY_API_KEY"

func webSearchTool(ctx context.Context, input string) (string, error) {
	search, err := tavilySearchFromContext(ctx)
	if err != nil {
		return "", fmt.Errorf("web_search unavailable: %w", err)
	}

	var request struct {
		Query string `json:"query"`
	}
	if err := decodeToolRequest(input, "web_search", &request); err != nil {
		return "", err
	}
	request.Query = strings.TrimSpace(request.Query)
	if request.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	return search.Call(ctx, request.Query)
}

func tavilySearchFromContext(ctx context.Context) (*tool.TavilySearch, error) {
	apiKey := strings.TrimSpace(core.EnvironmentVariableFromContext(ctx, tavilyAPIKeyEnvironment))
	if apiKey == "" {
		return nil, fmt.Errorf("%s not set", tavilyAPIKeyEnvironment)
	}
	return tool.NewTavilySearch(apiKey)
}

func NewWebSearch() Tool {
	return Tool{
		Function: &llms.FunctionDefinition{
			Name: "web_search",
			Description: "- Allows the assistant to search the web and use the results to inform responses\n" +
				"- Provides up-to-date information for current events and recent data\n" +
				"- Returns search result information including links and snippets\n" +
				"- Use this tool for accessing information beyond model knowledge",
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
		Handler: webSearchTool,
	}
}
