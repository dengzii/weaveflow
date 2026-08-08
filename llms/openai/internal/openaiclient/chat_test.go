package openaiclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStreamingChatAggregatesChoicesAndParallelToolCalls(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"id\":\"chatcmpl-1\",\"model\":\"test\",\"choices\":[{\"index\":1,\"delta\":{\"content\":\"second\"}}]}\n\n"))
		_, _ = writer.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_0\",\"type\":\"function\",\"function\":{\"name\":\"first\",\"arguments\":\"{\"}},{\"index\":1,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"second\",\"arguments\":\"{\"}}]}}]}\n\n"))
		_, _ = writer.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":1,\"function\":{\"arguments\":\"\\\"value\\\":2}\"}},{\"index\":0,\"function\":{\"arguments\":\"\\\"value\\\":1}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n"))
		_, _ = writer.Write([]byte("data: {\"choices\":[{\"index\":1,\"delta\":{\"content\":\" choice\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":4,\"total_tokens\":7}}\n\n"))
		_, _ = writer.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client, err := New("token", "test", server.URL+"/v1", "", APITypeOpenAI, "", server.Client(), "", nil, "openai", nil, nil)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	callbackCount := 0
	response, err := client.CreateChat(context.Background(), &ChatRequest{
		StreamingFunc: func(_ context.Context, _ []byte) error {
			callbackCount++
			return nil
		},
	})
	if err != nil {
		t.Fatalf("create chat: %v", err)
	}
	if len(response.Choices) != 2 || response.Choices[0].Index != 0 || response.Choices[1].Index != 1 {
		t.Fatalf("choices = %#v", response.Choices)
	}
	toolCalls := response.Choices[0].Message.ToolCalls
	if len(toolCalls) != 2 {
		t.Fatalf("tool calls = %#v", toolCalls)
	}
	if toolCalls[0].Function.Name != "first" || toolCalls[0].Function.Arguments != "{\"value\":1}" {
		t.Fatalf("first tool call = %#v", toolCalls[0])
	}
	if toolCalls[1].Function.Name != "second" || toolCalls[1].Function.Arguments != "{\"value\":2}" {
		t.Fatalf("second tool call = %#v", toolCalls[1])
	}
	if response.Choices[1].Message.Content != "second choice" || callbackCount != 2 {
		t.Fatalf("second choice = %#v, callback count = %d", response.Choices[1], callbackCount)
	}
	if response.Usage.TotalTokens != 7 {
		t.Fatalf("usage = %#v", response.Usage)
	}
}

func TestStreamingChatReturnsMalformedAndProviderErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "malformed", data: "not-json", want: "decode streaming response"},
		{name: "provider error", data: `{\"error\":{\"code\":\"bad_request\",\"message\":\"unsupported option\"}}`, want: "unsupported option"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = writer.Write([]byte("data: " + test.data + "\n\n"))
			}))
			defer server.Close()
			client, err := New("token", "test", server.URL, "", APITypeOpenAI, "", server.Client(), "", nil, "openai", nil, nil)
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			_, err = client.CreateChat(context.Background(), &ChatRequest{StreamingFunc: func(context.Context, []byte) error { return nil }})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestAzureURLFormats(t *testing.T) {
	t.Parallel()

	legacy, err := New("token", "deployment", "https://resource.openai.azure.com", "", APITypeAzure, "2024-10-21", http.DefaultClient, "", nil, "azure", nil, nil)
	if err != nil {
		t.Fatalf("new legacy client: %v", err)
	}
	if got := legacy.buildURL("/chat/completions", "deployment"); got != "https://resource.openai.azure.com/openai/deployments/deployment/chat/completions?api-version=2024-10-21" {
		t.Fatalf("legacy URL = %q", got)
	}
	v1, err := New("token", "gpt-5", "https://resource.openai.azure.com/openai/v1", "", APITypeAzure, "", http.DefaultClient, "", nil, "azure", nil, nil)
	if err != nil {
		t.Fatalf("new v1 client: %v", err)
	}
	if got := v1.buildURL("/responses", "gpt-5"); got != "https://resource.openai.azure.com/openai/v1/responses" {
		t.Fatalf("v1 URL = %q", got)
	}
}
