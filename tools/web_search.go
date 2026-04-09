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
			Description: "Search the public web and return the top matching results with title, URL, and snippet. " +
				"Use this when the user asks for external or recent information.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "The web search query.",
					},
				},
				"required":             []string{"query"},
				"additionalProperties": false,
			},
		},
		Handler: w.webSearchTool,
	}
}
