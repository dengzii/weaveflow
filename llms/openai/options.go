package openai

import "github.com/tmc/langchaingo/llms"

// WithMaxCompletionTokens sets the max_completion_tokens field for token generation.
// This is the recommended way to limit tokens with OpenAI models.
//
// Usage:
//
//	llm.GenerateContent(ctx, messages,
//	    openai.WithMaxCompletionTokens(100),
//	)
func WithMaxCompletionTokens(maxTokens int) llms.CallOption {
	return func(opts *llms.CallOptions) {
		opts.MaxTokens = maxTokens
	}
}
