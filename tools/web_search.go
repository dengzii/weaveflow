package tools

import (
	"context"
	"os"

	"github.com/smallnest/langgraphgo/tool"
	"github.com/tmc/langchaingo/llms"
)

type webSearch struct {
	tavily *tool.TavilySearch
}

func (w *webSearch) webSearchTool(ctx context.Context, input string) (string, error) {
	return w.tavily.Call(ctx, input)
}

func NewWebSearch() Tool {

	key, hasKey := os.LookupEnv("TAVILY_API_KEY")
	if !hasKey {
		panic("TAVILY_API_KEY not set")
	}
	search, err := tool.NewTavilySearch(key)
	if err != nil {
		panic(err)
	}
	w := webSearch{tavily: search}
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
					"allowed_domains": map[string]any{
						"type":        "array",
						"description": "Only include search results from these domains",
						"items": map[string]any{
							"type": "string",
						},
					},
					"blocked_domains": map[string]any{
						"type":        "array",
						"description": "Never include search results from these domains",
						"items": map[string]any{
							"type": "string",
						},
					},
				},
				"required":             []string{"query"},
				"additionalProperties": false,
			},
		},
		Handler: w.webSearchTool,
	}
}
