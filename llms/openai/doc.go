// Package openai provides an interface to OpenAI and OpenAI-compatible language models.
//
// Provider profiles select request field variants for Azure OpenAI, DeepSeek,
// Gemini, vLLM, Mistral, xAI, and OpenRouter. API format options select either
// Chat Completions or Responses requests.
//
// # Token Limits
//
// For setting token limits, use openai.WithMaxCompletionTokens(). The adapter
// sends max_completion_tokens, max_tokens, or max_output_tokens according to
// the configured provider and API format.
//
//	// Recommended for clarity:
//	llm.GenerateContent(ctx, messages,
//	    openai.WithMaxCompletionTokens(100),
//	)
package openai
