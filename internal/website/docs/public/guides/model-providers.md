# Configure Model Providers

WeaveFlow uses an OpenAI-compatible client for model-driven nodes. It supports OpenAI, Azure, DeepSeek, Gemini, vLLM,
Mistral, xAI, and OpenRouter, with either the Chat Completions or Responses request format.

## Environment variables for examples

The simplest local setup is:

```bash
export OPENAI_API_KEY="your-key"
export OPENAI_BASE_URL="https://api.openai.com/v1"
export OPENAI_MODEL="your-model"
```

`OPENAI_BASE_URL` is optional for the default OpenAI endpoint. Keep credentials in a local `.env` file or a secret
manager. Never commit them, put them in a Graph Definition, or print them in logs.

## Configure a provider explicitly

When a provider needs request-specific fields, configure the client explicitly in Go:

```go
model, err := openai.New(
    openai.WithToken(os.Getenv("OPENAI_API_KEY")),
    openai.WithModel(os.Getenv("OPENAI_MODEL")),
    openai.WithBaseURL(os.Getenv("OPENAI_BASE_URL")),
    openai.WithProvider(openai.ProviderDeepSeek),
    openai.WithAPIFormat(openai.APIFormatChatCompletions),
)
if err != nil {
    log.Fatal(err)
}
```

The default client uses the Chat Completions format. Select `openai.APIFormatResponses` when the endpoint implements the
Responses API. The provider and format must match the endpoint you are calling.

## Server-managed assistant

`cmd/server` can expose the optional Assistant API when all required variables are present:

```bash
export WEAVEFLOW_ASSISTANT_API_KEY="your-key"
export WEAVEFLOW_ASSISTANT_MODEL="your-model"
export WEAVEFLOW_ASSISTANT_BASE_URL="https://api.openai.com/v1"
export WEAVEFLOW_ASSISTANT_PROVIDER="openai"
export WEAVEFLOW_ASSISTANT_API_FORMAT="responses"
go run ./cmd/server -data .local/server
```

Assistant configuration is independent of the model IDs referenced by a Graph Definition. A graph node can select a model
from the runtime model context with its `model_id`; use the Workbench settings or your own server integration to provide
multiple models.

## Compatibility checklist

1. Verify the base URL points to the API root, commonly ending in `/v1`.
2. Verify the model ID is accepted by the provider.
3. Choose the request format (`chat_completions` or `responses`) supported by that endpoint.
4. Run a small `text_generation` or `llm_turn` graph before enabling tools or long plans.
5. Inspect the Run Events and provider error details if the request fails.

For a credential-free first run, use the [model-free examples](/guides/examples#runtime-control-without-a-model).
