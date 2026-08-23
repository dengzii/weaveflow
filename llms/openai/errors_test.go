package openai

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/llms"
)

func TestMapErrorClassifiesTransportFailures(t *testing.T) {
	tests := []struct {
		message string
		class   core.ErrorClass
	}{
		{message: "request timeout: API call exceeded deadline", class: core.ErrorTimeout},
		{message: "network error: failed to reach API server", class: core.ErrorUnavailable},
		{message: "API returned unexpected status code: 500", class: core.ErrorUnavailable},
		{message: "API returned unexpected status code: 504", class: core.ErrorUnavailable},
		{message: "request cancelled", class: core.ErrorCanceled},
	}
	for _, test := range tests {
		t.Run(test.message, func(t *testing.T) {
			if got := core.ClassifyError(MapError(errors.New(test.message))); got != test.class {
				t.Fatalf("MapError() class = %q, want %q", got, test.class)
			}
		})
	}
}

func TestGenerateReportsProviderHTTPErrorDetails(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-Request-ID", "req_diagnostic_123")
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{
			"error":{
				"message":"temperature is not supported for this model; credential=secret-token-not-for-errors",
				"type":"invalid_request_error",
				"code":"unsupported_parameter",
				"param":"temperature"
			}
		}`))
	}))
	defer server.Close()

	model, err := New(
		WithToken("secret-token-not-for-errors"),
		WithModel("diagnostic-model"),
		WithProvider(ProviderDeepSeek),
		WithBaseURL(server.URL+"/v1"),
		WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("new model: %v", err)
	}
	_, err = model.Generate(context.Background(), llms.ModelRequest{
		Mode:     llms.ModelModeChat,
		Messages: []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "question")},
	})
	if err == nil {
		t.Fatal("generate content returned nil error")
	}

	for _, expected := range []string{
		`deepseek chat completion request failed for model "diagnostic-model"`,
		"HTTP 400 Bad Request",
		"temperature is not supported for this model; credential=[REDACTED]",
		"type=invalid_request_error",
		"code=unsupported_parameter",
		"param=temperature",
		"request_id=req_diagnostic_123",
	} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("error = %q, want %q", err, expected)
		}
	}
	if strings.Contains(err.Error(), "secret-token-not-for-errors") {
		t.Fatalf("error leaked token: %q", err)
	}
	if class := core.ClassifyError(err); class != core.ErrorInvalidInput {
		t.Fatalf("error class = %q, want %q", class, core.ErrorInvalidInput)
	}

	var executionErr core.ExecutionError
	if !errors.As(err, &executionErr) {
		t.Fatalf("error type = %T, want core.ExecutionError", err)
	}
	details := executionErr.Details()
	assertErrorDetail(t, details, "provider", "deepseek")
	assertErrorDetail(t, details, "model", "diagnostic-model")
	assertErrorDetail(t, details, "operation", "chat completion")
	assertErrorDetail(t, details, "api_format", "chat_completions")
	assertErrorDetail(t, details, "status_code", 400)
	assertErrorDetail(t, details, "provider_message", "temperature is not supported for this model; credential=[REDACTED]")
	assertErrorDetail(t, details, "provider_error_type", "invalid_request_error")
	assertErrorDetail(t, details, "provider_error_code", "unsupported_parameter")
	assertErrorDetail(t, details, "provider_error_param", "temperature")
	assertErrorDetail(t, details, "provider_request_id", "req_diagnostic_123")
}

func TestGenerateReportsNetworkCauseWithoutRequestURL(t *testing.T) {
	t.Parallel()

	requestURL := "https://user:password@api.example.test/v1/chat/completions?api_key=query-secret"
	model, err := New(
		WithToken("header-secret"),
		WithModel("diagnostic-model"),
		WithBaseURL("https://api.example.test/v1"),
		WithHTTPClient(networkFailureDoer{err: &url.Error{
			Op:  "Post",
			URL: requestURL,
			Err: &net.DNSError{Err: "no such host", Name: "api.example.test", IsNotFound: true},
		}}),
	)
	if err != nil {
		t.Fatalf("new model: %v", err)
	}
	_, err = model.Generate(context.Background(), llms.ModelRequest{
		Mode:     llms.ModelModeChat,
		Messages: []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "question")},
	})
	if err == nil {
		t.Fatal("generate content returned nil error")
	}

	if !strings.Contains(err.Error(), "lookup api.example.test: no such host") {
		t.Fatalf("error = %q, want DNS cause", err)
	}
	for _, secret := range []string{"user:password", "query-secret", "header-secret", requestURL} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked %q: %q", secret, err)
		}
	}
	if class := core.ClassifyError(err); class != core.ErrorUnavailable {
		t.Fatalf("error class = %q, want %q", class, core.ErrorUnavailable)
	}
}

type networkFailureDoer struct {
	err error
}

func (doer networkFailureDoer) Do(*http.Request) (*http.Response, error) {
	return nil, doer.err
}

func assertErrorDetail(t *testing.T, details map[string]any, key string, expected any) {
	t.Helper()
	if actual := details[key]; actual != expected {
		t.Fatalf("details[%q] = %#v, want %#v", key, actual, expected)
	}
}
