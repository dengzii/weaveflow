package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tmc/langchaingo/llms"
)

func TestProviderSpecificChatRequestFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		provider Provider
		options  []llms.CallOption
		assert   func(*testing.T, map[string]any)
	}{
		{
			name:     "openai sampling",
			provider: ProviderOpenAI,
			options: []llms.CallOption{
				llms.WithMaxTokens(48),
				llms.WithTopP(0.8),
			},
			assert: func(t *testing.T, request map[string]any) {
				t.Helper()
				if request["max_completion_tokens"] != float64(48) || request["top_p"] != 0.8 {
					t.Fatalf("OpenAI request = %#v", request)
				}
				if _, exists := request["temperature"]; exists {
					t.Fatalf("OpenAI request contains an implicit temperature: %#v", request)
				}
			},
		},
		{
			name:     "deepseek thinking",
			provider: ProviderDeepSeek,
			options: []llms.CallOption{
				llms.WithMaxTokens(64),
				llms.WithThinkingMode(llms.ThinkingModeHigh),
			},
			assert: func(t *testing.T, request map[string]any) {
				t.Helper()
				if request["max_tokens"] != float64(64) {
					t.Fatalf("max_tokens = %#v", request["max_tokens"])
				}
				thinking, _ := request["thinking"].(map[string]any)
				if thinking["type"] != "enabled" {
					t.Fatalf("thinking = %#v", thinking)
				}
				if _, exists := request["reasoning_effort"]; exists {
					t.Fatalf("DeepSeek request contains reasoning_effort: %#v", request)
				}
			},
		},
		{
			name:     "mistral random seed",
			provider: ProviderMistral,
			options: []llms.CallOption{
				llms.WithMaxTokens(32),
				llms.WithSeed(7),
			},
			assert: func(t *testing.T, request map[string]any) {
				t.Helper()
				if request["max_tokens"] != float64(32) || request["random_seed"] != float64(7) {
					t.Fatalf("Mistral request = %#v", request)
				}
				if _, exists := request["seed"]; exists {
					t.Fatalf("Mistral request contains seed: %#v", request)
				}
			},
		},
		{
			name:     "openrouter reasoning",
			provider: ProviderOpenRouter,
			options: []llms.CallOption{
				llms.WithThinkingMode(llms.ThinkingModeMedium),
			},
			assert: func(t *testing.T, request map[string]any) {
				t.Helper()
				reasoning, _ := request["reasoning"].(map[string]any)
				if reasoning["effort"] != "medium" {
					t.Fatalf("OpenRouter reasoning = %#v", reasoning)
				}
				if _, exists := request["reasoning_effort"]; exists {
					t.Fatalf("OpenRouter request contains reasoning_effort: %#v", request)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var captured map[string]any
			server := newChatTestServer(t, &captured)
			defer server.Close()
			model, err := New(
				WithToken("test-token"),
				WithModel("provider-model"),
				WithProvider(test.provider),
				WithBaseURL(server.URL+"/v1"),
				WithHTTPClient(server.Client()),
			)
			if err != nil {
				t.Fatalf("new model: %v", err)
			}
			if _, err := model.GenerateContent(
				context.Background(),
				[]llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "question")},
				test.options...,
			); err != nil {
				t.Fatalf("generate content: %v", err)
			}
			test.assert(t, captured)
		})
	}
}

func TestAzureProviderUsesV1URLAndAPIKeyHeader(t *testing.T) {
	t.Parallel()

	var capturedPath string
	var capturedAPIKey string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		capturedPath = request.URL.Path
		capturedAPIKey = request.Header.Get("api-key")
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"id":"chatcmpl-1",
			"object":"chat.completion",
			"model":"deployment",
			"choices":[{"index":0,"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}]
		}`))
	}))
	defer server.Close()

	model, err := New(
		WithToken("azure-key"),
		WithModel("deployment"),
		WithProvider(ProviderAzure),
		WithBaseURL(server.URL+"/openai/v1"),
		WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("new model: %v", err)
	}
	if _, err := model.GenerateContent(
		context.Background(),
		[]llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "question")},
	); err != nil {
		t.Fatalf("generate content: %v", err)
	}
	if capturedPath != "/openai/v1/chat/completions" || capturedAPIKey != "azure-key" {
		t.Fatalf("Azure path = %q, api-key = %q", capturedPath, capturedAPIKey)
	}
}

func TestResponsesRequestAndResultMapping(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	var capturedPath string
	var capturedHeader string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		capturedPath = request.URL.Path
		capturedHeader = request.Header.Get("X-Provider-Option")
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"id":"resp_1",
			"status":"completed",
			"model":"gpt-5-test",
			"output":[
				{"type":"reasoning","summary":[{"type":"summary_text","text":"summary"}]},
				{"type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"answer"}]},
				{"type":"function_call","status":"completed","call_id":"call_1","name":"lookup","arguments":"{\"q\":\"x\"}"}
			],
			"usage":{"input_tokens":10,"output_tokens":7,"total_tokens":17,"input_tokens_details":{"cached_tokens":4},"output_tokens_details":{"reasoning_tokens":3}}
		}`))
	}))
	defer server.Close()

	model, err := New(
		WithToken("test-token"),
		WithModel("gpt-5-test"),
		WithAPIFormat(APIFormatResponses),
		WithBaseURL(server.URL+"/v1"),
		WithExtraHeaders(map[string]string{"X-Provider-Option": "enabled"}),
		WithResponseFormat(&ResponseFormat{
			Type: "json_schema",
			JSONSchema: &ResponseFormatJSONSchema{
				Name:   "answer",
				Strict: true,
				Schema: &ResponseFormatJSONSchemaProperty{Type: "object"},
			},
		}),
		WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("new model: %v", err)
	}
	var streamed string
	response, err := model.GenerateContent(
		context.Background(),
		[]llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, "follow policy"),
			llms.TextParts(llms.ChatMessageTypeHuman, "question"),
		},
		llms.WithMaxTokens(128),
		llms.WithThinkingMode(llms.ThinkingModeHigh),
		llms.WithTools([]llms.Tool{{
			Type: "function",
			Function: &llms.FunctionDefinition{
				Name:       "lookup",
				Parameters: map[string]any{"type": "object"},
				Strict:     true,
			},
		}}),
		WithParallelToolCalls(true),
		WithServiceTier("priority"),
		WithStore(false),
		WithVerbosity("low"),
		WithPromptCacheKey("cache-key"),
		WithSafetyIdentifier("user-1"),
		WithRequestExtraBody(map[string]any{"include": []any{"reasoning.encrypted_content"}}),
		llms.WithStreamingFunc(func(_ context.Context, chunk []byte) error {
			streamed += string(chunk)
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("generate content: %v", err)
	}

	if capturedPath != "/v1/responses" || capturedHeader != "enabled" {
		t.Fatalf("path = %q, header = %q", capturedPath, capturedHeader)
	}
	if captured["max_output_tokens"] != float64(128) || captured["parallel_tool_calls"] != true {
		t.Fatalf("Responses generation fields = %#v", captured)
	}
	reasoning, _ := captured["reasoning"].(map[string]any)
	if reasoning["effort"] != "high" {
		t.Fatalf("reasoning = %#v", reasoning)
	}
	text, _ := captured["text"].(map[string]any)
	format, _ := text["format"].(map[string]any)
	if text["verbosity"] != "low" || format["name"] != "answer" || format["type"] != "json_schema" {
		t.Fatalf("text config = %#v", text)
	}
	inputs, _ := captured["input"].([]any)
	firstInput, _ := inputs[0].(map[string]any)
	if firstInput["role"] != "developer" {
		t.Fatalf("first input = %#v, want developer role", firstInput)
	}
	tools, _ := captured["tools"].([]any)
	firstTool, _ := tools[0].(map[string]any)
	if firstTool["name"] != "lookup" || firstTool["function"] != nil {
		t.Fatalf("Responses tool = %#v", firstTool)
	}
	if streamed != "answer" {
		t.Fatalf("streamed = %q, want answer", streamed)
	}
	if response == nil || len(response.Choices) != 1 {
		t.Fatalf("response = %#v", response)
	}
	choice := response.Choices[0]
	if choice.Content != "answer" || choice.ReasoningContent != "summary" || choice.StopReason != "tool_calls" {
		t.Fatalf("choice = %#v", choice)
	}
	if len(choice.ToolCalls) != 1 || choice.ToolCalls[0].ID != "call_1" {
		t.Fatalf("tool calls = %#v", choice.ToolCalls)
	}
	if choice.GenerationInfo["PromptCachedTokens"] != 4 {
		t.Fatalf("generation info = %#v", choice.GenerationInfo)
	}
}

func newChatTestServer(t *testing.T, captured *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(captured); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"id":"chatcmpl-1",
			"object":"chat.completion",
			"model":"provider-model",
			"choices":[{"index":0,"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}
		}`))
	}))
}
