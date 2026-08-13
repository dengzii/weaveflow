package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dengzii/weaveflow/llms"
)

func TestGenerateCompletionUsesTextCompletionsEndpoint(t *testing.T) {
	t.Parallel()

	type capturedRequest struct {
		Model       string   `json:"model"`
		Prompt      string   `json:"prompt"`
		MaxTokens   int      `json:"max_tokens"`
		Temperature float64  `json:"temperature"`
		Stop        []string `json:"stop"`
	}
	var captured capturedRequest
	var capturedPath string
	var capturedAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		capturedPath = request.URL.Path
		capturedAuthorization = request.Header.Get("Authorization")
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"id":"cmpl-1",
			"object":"text_completion",
			"model":"code-model",
			"choices":[{"index":0,"text":" completed","finish_reason":"stop"}],
			"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}
		}`))
	}))
	defer server.Close()

	model, err := New(
		WithToken("test-token"),
		WithModel("code-model"),
		WithBaseURL(server.URL+"/v1"),
		WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("new model: %v", err)
	}
	temperature := 0.25
	response, err := model.Generate(context.Background(), llms.ModelRequest{
		Mode:        llms.ModelModeCompletion,
		Prompt:      "func add(a, b int) int {",
		MaxTokens:   64,
		Temperature: &temperature,
		StopWords:   []string{"\n\n"},
	})
	if err != nil {
		t.Fatalf("generate completion: %v", err)
	}

	if capturedPath != "/v1/completions" {
		t.Fatalf("request path = %q, want /v1/completions", capturedPath)
	}
	if capturedAuthorization != "Bearer test-token" {
		t.Fatalf("authorization = %q", capturedAuthorization)
	}
	if captured.Model != "code-model" || captured.Prompt != "func add(a, b int) int {" {
		t.Fatalf("request = %#v", captured)
	}
	if captured.MaxTokens != 64 || captured.Temperature != 0.25 {
		t.Fatalf("generation options = %#v", captured)
	}
	if len(captured.Stop) != 1 || captured.Stop[0] != "\n\n" {
		t.Fatalf("stop sequences = %#v", captured.Stop)
	}
	if response == nil || len(response.Choices) != 1 || response.Choices[0].Content != " completed" {
		t.Fatalf("response = %#v", response)
	}
	if response.Usage.TotalTokens != 5 {
		t.Fatalf("total tokens = %d, want 5", response.Usage.TotalTokens)
	}
}

func TestGenerateCompletionWithReasoningUsesChatEndpoint(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	var capturedPath string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		capturedPath = request.URL.Path
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"id":"chatcmpl-1",
			"object":"chat.completion",
			"model":"gpt-5-test",
			"choices":[{"index":0,"message":{"role":"assistant","content":"completed"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}
		}`))
	}))
	defer server.Close()

	model, err := New(
		WithToken("test-token"),
		WithModel("gpt-5-test"),
		WithBaseURL(server.URL+"/v1"),
		WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("new model: %v", err)
	}
	response, err := model.Generate(context.Background(), llms.ModelRequest{
		Mode:     llms.ModelModeCompletion,
		Prompt:   "reason about this",
		Thinking: llms.ThinkingModeHigh,
	})
	if err != nil {
		t.Fatalf("generate completion: %v", err)
	}

	if capturedPath != "/v1/chat/completions" {
		t.Fatalf("request path = %q, want /v1/chat/completions", capturedPath)
	}
	if captured["reasoning_effort"] != "high" {
		t.Fatalf("reasoning effort = %#v, want high", captured["reasoning_effort"])
	}
	if _, exists := captured["prompt"]; exists {
		t.Fatalf("chat request unexpectedly contains prompt: %#v", captured)
	}
	if response == nil || len(response.Choices) != 1 || response.Choices[0].Content != "completed" {
		t.Fatalf("response = %#v", response)
	}
}

func TestGenerateContentSendsReasoningEffort(t *testing.T) {
	t.Parallel()

	type capturedRequest struct {
		ReasoningEffort string `json:"reasoning_effort"`
	}
	var captured capturedRequest
	var capturedPath string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		capturedPath = request.URL.Path
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"id":"chatcmpl-1",
			"object":"chat.completion",
			"model":"gpt-5-test",
			"choices":[{"index":0,"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}
		}`))
	}))
	defer server.Close()

	model, err := New(
		WithToken("test-token"),
		WithModel("gpt-5-test"),
		WithBaseURL(server.URL+"/v1"),
		WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("new model: %v", err)
	}
	response, err := model.Generate(context.Background(), llms.ModelRequest{
		Mode:     llms.ModelModeChat,
		Messages: []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "question")},
		Thinking: llms.ThinkingModeMedium,
	})
	if err != nil {
		t.Fatalf("generate content: %v", err)
	}

	if capturedPath != "/v1/chat/completions" {
		t.Fatalf("request path = %q, want /v1/chat/completions", capturedPath)
	}
	if captured.ReasoningEffort != "medium" {
		t.Fatalf("reasoning effort = %q, want medium", captured.ReasoningEffort)
	}
	if response == nil || len(response.Choices) != 1 || response.Choices[0].Content != "answer" {
		t.Fatalf("response = %#v", response)
	}
}
