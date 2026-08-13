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

func (generate modelFunc) Generate(ctx context.Context, request llms.ModelRequest) (*llms.ModelResponse, error) {
	return generate(ctx, request)
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
	if err == nil || !strings.Contains(err.Error(), "multiple JSON values") {
		t.Fatalf("GenerateModel() error = %v, want multiple JSON values", err)
	}
	if got != response || got.Cost == nil {
		t.Fatalf("response = %#v", got)
	}
	if got.Cost.Currency != "USD" || math.Abs(got.Cost.Total-1.9) > 1e-9 {
		t.Fatalf("cost = %#v, want USD 1.9", got.Cost)
	}
	if failedEvent.Response != response || failedEvent.Response.Cost == nil || failedEvent.Err == nil {
		t.Fatalf("failed observer event = %#v", failedEvent)
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
