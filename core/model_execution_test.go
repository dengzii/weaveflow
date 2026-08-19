package core

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/llms"
	"github.com/dengzii/weaveflow/state"
)

type modelFunc func(context.Context, llms.ModelRequest) (*llms.ModelResponse, error)

type mutableObserverError struct {
	message string
}

func (observerErr *mutableObserverError) Error() string {
	return observerErr.message
}

func (generate modelFunc) Generate(ctx context.Context, request llms.ModelRequest) (*llms.ModelResponse, error) {
	return generate(ctx, request)
}

func TestModelObserverCannotMutateReturnedError(t *testing.T) {
	originalErr := &mutableObserverError{message: "provider failed"}
	ctx := WithModelCallObserver(context.Background(), func(_ context.Context, event ModelCallEvent) error {
		if event.Stage != ModelCallFailed {
			return nil
		}
		if observedErr, ok := event.Err.(*mutableObserverError); ok {
			observedErr.message = "observer changed error"
		}
		return nil
	})
	_, err := GenerateModel(ctx, modelFunc(func(context.Context, llms.ModelRequest) (*llms.ModelResponse, error) {
		return nil, originalErr
	}), llms.ModelRequest{Mode: llms.ModelModeCompletion, Prompt: "test"})
	if err != originalErr {
		t.Fatalf("GenerateModel() error = %v, want original error", err)
	}
	if originalErr.message != "provider failed" {
		t.Fatalf("provider error message = %q", originalErr.message)
	}
}

type modelObserverOpaqueValue struct {
	values []string
}

func TestGenerateModelRejectsTrailingStructuredJSONAndReportsCost(t *testing.T) {
	response := &llms.ModelResponse{
		Choices: []*llms.ModelChoice{{Content: `{"answer":"ok"} {"extra":true}`}},
		Usage: llms.ModelUsage{
			InputTokens:       1_000_000,
			CachedInputTokens: 200_000,
			OutputTokens:      500_000,
		},
	}
	var failedEvent ModelCallEvent
	ctx := WithModelConfigs(context.Background(), map[string]ModelConfig{
		"priced": {
			Pricing: llms.ModelPricing{
				Currency:              "usd",
				InputPerMillion:       1,
				CachedInputPerMillion: 0.5,
				OutputPerMillion:      2,
			},
		},
	})
	ctx = WithModelCallObserver(ctx, func(_ context.Context, event ModelCallEvent) error {
		if event.Stage == ModelCallFailed {
			failedEvent = event
		}
		return nil
	})

	got, err := GenerateModel(ctx, modelFunc(func(context.Context, llms.ModelRequest) (*llms.ModelResponse, error) {
		return response, nil
	}), llms.ModelRequest{
		ModelID: "priced",
		Mode:    llms.ModelModeChat,
		Messages: []llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeHuman, "return JSON"),
		},
		ResponseSchema: state.JSONSchema{
			"type": "object",
			"properties": state.JSONSchema{
				"answer": state.JSONSchema{"type": "string"},
			},
			"required":             []string{"answer"},
			"additionalProperties": false,
		},
	})
	if err == nil || ClassifyError(err) != ErrorInvalidOutput || !strings.Contains(err.Error(), "multiple JSON values") {
		t.Fatalf("GenerateModel() error = %v, want multiple JSON values", err)
	}
	if got != response || got.Cost == nil {
		t.Fatalf("response = %#v", got)
	}
	if got.Cost.Currency != "USD" || math.Abs(got.Cost.Total-1.9) > 1e-9 {
		t.Fatalf("cost = %#v, want USD 1.9", got.Cost)
	}
	if failedEvent.Response == nil || failedEvent.Response == response || failedEvent.Response.Cost == nil || failedEvent.Err == nil {
		t.Fatalf("failed observer event = %#v", failedEvent)
	}
}

func TestGenerateModelAllowsStructuredToolCallChoiceWithoutContent(t *testing.T) {
	response := &llms.ModelResponse{Choices: []*llms.ModelChoice{{
		ToolCalls: []llms.ToolCall{{
			ID:   "lookup",
			Type: "function",
			FunctionCall: &llms.FunctionCall{
				Name:      "lookup",
				Arguments: []byte(`{"query":"status"}`),
			},
		}},
	}}}
	got, err := GenerateModel(context.Background(), modelFunc(func(context.Context, llms.ModelRequest) (*llms.ModelResponse, error) {
		return response, nil
	}), llms.ModelRequest{
		Mode:     llms.ModelModeChat,
		Messages: []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "use a tool")},
		Tools: []llms.ToolDefinition{{Function: &llms.FunctionDefinition{
			Name:       "lookup",
			Parameters: state.JSONSchema{"type": "object"},
		}}},
		ResponseSchema: state.JSONSchema{
			"type": "object",
			"properties": state.JSONSchema{
				"answer": state.JSONSchema{"type": "string"},
			},
			"required": []string{"answer"},
		},
	})
	if err != nil {
		t.Fatalf("GenerateModel() error = %v", err)
	}
	if got != response {
		t.Fatalf("GenerateModel() response = %#v, want original response", got)
	}
}

func TestDecodeStructuredOutputCompatibility(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		content    string
		schema     state.JSONSchema
		normalized string
	}{
		{
			name:    "markdown JSON fence",
			content: "Here is the result:\n```json\n{\n  \"answer\": 7\n}\n```\nDone.",
			schema: state.JSONSchema{
				"type": "object",
				"properties": state.JSONSchema{
					"answer": state.JSONSchema{"type": "integer"},
				},
				"required": []string{"answer"},
			},
			normalized: `{"answer":7}`,
		},
		{
			name:       "prefixed and suffixed array",
			content:    "Result follows: [1, 2, 3]\nThis is the final list.",
			schema:     state.JSONSchema{"type": "array", "items": state.JSONSchema{"type": "integer"}},
			normalized: `[1,2,3]`,
		},
		{
			name:       "labeled scalar",
			content:    "The final value is 7.",
			schema:     state.JSONSchema{"type": "integer"},
			normalized: `7`,
		},
		{
			name:       "labeled null",
			content:    "Final result: null",
			schema:     state.JSONSchema{"type": "null"},
			normalized: `null`,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			normalized, _, err := DecodeStructuredOutput(testCase.content, testCase.schema, true)
			if err != nil {
				t.Fatalf("DecodeStructuredOutput() error = %v", err)
			}
			if normalized != testCase.normalized {
				t.Fatalf("DecodeStructuredOutput() = %q, want %q", normalized, testCase.normalized)
			}
		})
	}
}

func TestDecodeStructuredOutputStrictModeRejectsExtraText(t *testing.T) {
	t.Parallel()

	_, _, err := DecodeStructuredOutput("Result: {\"answer\":7}", state.JSONSchema{
		"type": "object",
		"properties": state.JSONSchema{
			"answer": state.JSONSchema{"type": "integer"},
		},
	}, false)
	if err == nil || ClassifyError(err) != ErrorInvalidOutput {
		t.Fatalf("DecodeStructuredOutput() error = %v, want invalid_output", err)
	}
}

func TestGenerateModelRequiresJSONWithoutResponseSchema(t *testing.T) {
	t.Parallel()

	response := &llms.ModelResponse{Choices: []*llms.ModelChoice{{Content: "Result: [1, 2]"}}}
	got, err := GenerateModel(context.Background(), modelFunc(func(context.Context, llms.ModelRequest) (*llms.ModelResponse, error) {
		return response, nil
	}), llms.ModelRequest{
		Mode:                      llms.ModelModeChat,
		Messages:                  []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "return JSON")},
		ResponseJSON:              true,
		ResponseJSONCompatibility: true,
	})
	if err != nil {
		t.Fatalf("GenerateModel() error = %v", err)
	}
	if got != response {
		t.Fatalf("GenerateModel() response = %#v, want original response", got)
	}
}

func TestGenerateModelValidatesAnImmutableRequestSnapshot(t *testing.T) {
	responseSchema := state.JSONSchema{
		"type": "object",
		"properties": state.JSONSchema{
			"answer": state.JSONSchema{"type": "integer"},
		},
		"required":             []string{"answer"},
		"additionalProperties": false,
	}
	ctx := WithModelCallObserver(context.Background(), func(_ context.Context, event ModelCallEvent) error {
		if event.Stage == ModelCallStarted || event.Stage == ModelCallStream {
			event.Request.ResponseSchema = nil
		}
		return nil
	})
	_, err := GenerateModel(ctx, modelFunc(func(_ context.Context, request llms.ModelRequest) (*llms.ModelResponse, error) {
		request.ResponseSchema = nil
		if request.Stream != nil {
			if streamErr := request.Stream(context.Background(), llms.ModelStreamEvent{Text: "progress"}); streamErr != nil {
				return nil, streamErr
			}
		}
		return &llms.ModelResponse{Choices: []*llms.ModelChoice{{Content: `{"answer":"not-an-integer"}`}}}, nil
	}), llms.ModelRequest{
		Mode:           llms.ModelModeChat,
		Messages:       []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "validate")},
		ResponseSchema: responseSchema,
	})
	if err == nil || !strings.Contains(err.Error(), "model response") {
		t.Fatalf("GenerateModel() error = %v, want immutable schema validation failure", err)
	}
}

func TestGenerateModelObserverCannotMutateReturnedResponse(t *testing.T) {
	response := &llms.ModelResponse{Choices: []*llms.ModelChoice{{Content: `{"answer":7}`}}}
	ctx := WithModelCallObserver(context.Background(), func(_ context.Context, event ModelCallEvent) error {
		if event.Stage == ModelCallCompleted {
			event.Response.Choices[0].Content = `{"answer":"changed"}`
		}
		return nil
	})
	got, err := GenerateModel(ctx, modelFunc(func(context.Context, llms.ModelRequest) (*llms.ModelResponse, error) {
		return response, nil
	}), llms.ModelRequest{
		Mode:     llms.ModelModeChat,
		Messages: []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "validate")},
		ResponseSchema: state.JSONSchema{
			"type": "object",
			"properties": state.JSONSchema{
				"answer": state.JSONSchema{"type": "integer"},
			},
			"required": []string{"answer"},
		},
	})
	if err != nil {
		t.Fatalf("GenerateModel() error = %v", err)
	}
	if got != response || got.Choices[0].Content != `{"answer":7}` {
		t.Fatalf("observer mutated returned response: %#v", got)
	}
}

func TestGenerateModelRejectsOpaqueMutableRequestData(t *testing.T) {
	called := false
	_, err := GenerateModel(context.Background(), modelFunc(func(context.Context, llms.ModelRequest) (*llms.ModelResponse, error) {
		called = true
		return &llms.ModelResponse{}, nil
	}), llms.ModelRequest{
		Mode:            llms.ModelModeChat,
		Messages:        []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "validate")},
		ProviderOptions: map[string]any{"opaque": &modelObserverOpaqueValue{values: []string{"source"}}},
	})
	if err == nil || ClassifyError(err) != ErrorInvalidInput || !strings.Contains(err.Error(), "cannot be safely cloned") {
		t.Fatalf("GenerateModel() error = %v, want invalid opaque request", err)
	}
	if called {
		t.Fatal("GenerateModel() called provider with an unsafe request clone")
	}
}

func TestModelObserverOmitsOpaqueResponseMetadata(t *testing.T) {
	opaque := &modelObserverOpaqueValue{values: []string{"source"}}
	response := &llms.ModelResponse{
		Choices:  []*llms.ModelChoice{{Content: "ok"}},
		Metadata: map[string]any{"opaque": opaque},
	}
	var completed ModelCallEvent
	ctx := WithModelCallObserver(context.Background(), func(_ context.Context, event ModelCallEvent) error {
		if event.Stage == ModelCallCompleted {
			completed = event
		}
		return nil
	})
	got, err := GenerateModel(ctx, modelFunc(func(context.Context, llms.ModelRequest) (*llms.ModelResponse, error) {
		return response, nil
	}), llms.ModelRequest{
		Mode:     llms.ModelModeChat,
		Messages: []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "validate")},
	})
	if err != nil {
		t.Fatalf("GenerateModel() error = %v", err)
	}
	if completed.CloneError == nil || completed.Response == nil || completed.Response.Metadata != nil {
		t.Fatalf("completed observer event = %#v, clone error = %v", completed.Response, completed.CloneError)
	}
	if got != response || got.Metadata["opaque"] != opaque || opaque.values[0] != "source" {
		t.Fatalf("observer clone changed returned response: %#v", got)
	}
}

func TestModelConfigsDeepCloneExtraBody(t *testing.T) {
	extraBody := map[string]any{"nested": map[string]any{"value": "original"}}
	ctx := WithModelConfigs(context.Background(), map[string]ModelConfig{
		"default": {ExtraBody: extraBody},
	})
	extraBody["nested"].(map[string]any)["value"] = "changed"
	config, ok := ModelConfigByIDFromContext(ctx, "default")
	if !ok || config.ExtraBody["nested"].(map[string]any)["value"] != "original" {
		t.Fatalf("WithModelConfigs() retained caller alias: %#v", config.ExtraBody)
	}
	config.ExtraBody["nested"].(map[string]any)["value"] = "changed-again"
	again, _ := ModelConfigByIDFromContext(ctx, "default")
	if again.ExtraBody["nested"].(map[string]any)["value"] != "original" {
		t.Fatalf("ModelConfigsFromContext() exposed stored alias: %#v", again.ExtraBody)
	}
}

func TestGenerateModelStreamPropagation(t *testing.T) {
	t.Run("no consumer keeps provider request non-streaming", func(t *testing.T) {
		_, err := GenerateModel(context.Background(), modelFunc(func(_ context.Context, request llms.ModelRequest) (*llms.ModelResponse, error) {
			if request.Stream != nil {
				t.Fatal("GenerateModel injected a stream callback without a consumer")
			}
			return &llms.ModelResponse{Choices: []*llms.ModelChoice{{Content: "ok"}}}, nil
		}), llms.ModelRequest{
			Mode:     llms.ModelModeChat,
			Messages: []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "hello")},
		})
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("callback error stops provider", func(t *testing.T) {
		streamErr := errors.New("stop stream")
		_, err := GenerateModel(context.Background(), modelFunc(func(ctx context.Context, request llms.ModelRequest) (*llms.ModelResponse, error) {
			if request.Stream == nil {
				t.Fatal("stream callback is nil")
			}
			if err := request.Stream(ctx, llms.ModelStreamEvent{Type: llms.ModelStreamContent, Text: "partial"}); err != nil {
				return nil, err
			}
			return &llms.ModelResponse{}, nil
		}), llms.ModelRequest{
			Mode:     llms.ModelModeChat,
			Messages: []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "hello")},
			Stream: func(context.Context, llms.ModelStreamEvent) error {
				return streamErr
			},
		})
		if !errors.Is(err, streamErr) {
			t.Fatalf("GenerateModel() error = %v, want %v", err, streamErr)
		}
	})

	t.Run("canceled context reaches stream callback", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := GenerateModel(ctx, modelFunc(func(streamCtx context.Context, request llms.ModelRequest) (*llms.ModelResponse, error) {
			return nil, request.Stream(streamCtx, llms.ModelStreamEvent{Type: llms.ModelStreamContent, Text: "partial"})
		}), llms.ModelRequest{
			Mode:     llms.ModelModeChat,
			Messages: []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "hello")},
			Stream:   func(context.Context, llms.ModelStreamEvent) error { return nil },
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("GenerateModel() error = %v, want context canceled", err)
		}
	})
}
