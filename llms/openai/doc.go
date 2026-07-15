// Package openai provides an interface to OpenAI's language models.
//
// # Token Limits
//
// For setting token limits with OpenAI models, use openai.WithMaxCompletionTokens()
// for clarity. The OpenAI API now uses max_completion_tokens as the field for
// limiting output tokens.
//
//	// Recommended for clarity:
//	llm.GenerateContent(ctx, messages,
//	    openai.WithMaxCompletionTokens(100),
//	)
//
// The implementation always sends max_completion_tokens.
package openai
