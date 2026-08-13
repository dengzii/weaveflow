// Package openai provides an interface to OpenAI and OpenAI-compatible language models.
//
// Provider profiles select request field variants for Azure OpenAI, DeepSeek,
// Gemini, vLLM, Mistral, xAI, and OpenRouter. API format options select either
// Chat Completions or Responses requests.
//
// # Token Limits
//
// Set ModelRequest.MaxTokens to let the adapter select max_completion_tokens,
// max_tokens, or max_output_tokens for the configured provider and API format.
//
//	llm.Generate(ctx, llms.ModelRequest{Messages: messages, MaxTokens: 100})
package openai
