package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/smallnest/langgraphgo/tool"
	"github.com/tmc/langchaingo/llms"
)

type webSearch struct {
	tavily            *tool.TavilySearch
	initializationErr error
}

func (w *webSearch) webSearchTool(ctx context.Context, input string) (string, error) {
	if w.initializationErr != nil {
		return "", fmt.Errorf("web_search unavailable: %w", w.initializationErr)
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
	return w.tavily.Call(ctx, request.Query)
}

func NewWebSearch() Tool {
	search, err := tool.NewTavilySearch(strings.TrimSpace(os.Getenv("TAVILY_API_KEY")))
	w := webSearch{tavily: search, initializationErr: err}
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
		Handler: w.webSearchTool,
	}
}
