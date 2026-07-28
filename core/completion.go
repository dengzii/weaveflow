package core

import (
	"context"

	"github.com/tmc/langchaingo/llms"
)

// CompletionModel is implemented by models that support raw text completion.
type CompletionModel interface {
	GenerateCompletion(ctx context.Context, prompt string, options ...llms.CallOption) (*llms.ContentResponse, error)
}
