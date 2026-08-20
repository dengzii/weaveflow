package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/dengzii/weaveflow/core"

	"github.com/dengzii/weaveflow/llms"
)

const tavilyAPIKeyEnvironment = "TAVILY_API_KEY"

func webSearchTool(ctx context.Context, call llms.ToolCall) (llms.ToolResult, error) {
	search, err := tavilySearchFromContext(ctx)
	if err != nil {
		return llms.ToolResult{}, fmt.Errorf("web_search unavailable: %w", err)
	}

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
	content, err := search.Call(ctx, request.Query)
	if err != nil {
		return llms.ToolResult{}, err
	}
	return textToolResult(call, content), nil
}

type tavilySearch struct {
	apiKey string
}

func tavilySearchFromContext(ctx context.Context) (*tavilySearch, error) {
	apiKey := strings.TrimSpace(core.EnvironmentVariableFromContext(ctx, tavilyAPIKeyEnvironment))
	if apiKey == "" {
		return nil, fmt.Errorf("%s not set", tavilyAPIKeyEnvironment)
	}
	return &tavilySearch{apiKey: apiKey}, nil
}

func (search *tavilySearch) Call(ctx context.Context, query string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"query":        query,
		"api_key":      search.apiKey,
		"search_depth": "basic",
	})
	if err != nil {
		return "", fmt.Errorf("encode tavily request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.tavily.com/search", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create tavily request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("send tavily request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("tavily api returned status: %d", response.StatusCode)
	}
	var payload struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode tavily response: %w", err)
	}
	var output strings.Builder
	for _, result := range payload.Results {
		fmt.Fprintf(&output, "Title: %s\nURL: %s\nContent: %s\n\n", result.Title, result.URL, result.Content)
	}
	return output.String(), nil
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
