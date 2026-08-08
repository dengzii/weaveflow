package openai

import "github.com/tmc/langchaingo/llms"

const requestOptionsMetadataKey = "weaveflow.openai.request_options"

type requestOptions struct {
	ExtraBody         map[string]any
	ParallelToolCalls *bool
	ServiceTier       string
	Store             *bool
	Verbosity         string
	PromptCacheKey    string
	SafetyIdentifier  string
	DeveloperRole     bool
	Temperature       *float64
}

// WithMaxCompletionTokens limits generated tokens using the field required by
// the configured provider and API format.
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

func WithRequestExtraBody(extraBody map[string]any) llms.CallOption {
	return func(opts *llms.CallOptions) {
		requestOptionsFor(opts).ExtraBody = cloneAnyMap(extraBody)
	}
}

func WithParallelToolCalls(enabled bool) llms.CallOption {
	return func(opts *llms.CallOptions) {
		requestOptionsFor(opts).ParallelToolCalls = &enabled
	}
}

func WithServiceTier(serviceTier string) llms.CallOption {
	return func(opts *llms.CallOptions) {
		requestOptionsFor(opts).ServiceTier = serviceTier
	}
}

func WithStore(enabled bool) llms.CallOption {
	return func(opts *llms.CallOptions) {
		requestOptionsFor(opts).Store = &enabled
	}
}

func WithVerbosity(verbosity string) llms.CallOption {
	return func(opts *llms.CallOptions) {
		requestOptionsFor(opts).Verbosity = verbosity
	}
}

func WithPromptCacheKey(promptCacheKey string) llms.CallOption {
	return func(opts *llms.CallOptions) {
		requestOptionsFor(opts).PromptCacheKey = promptCacheKey
	}
}

func WithSafetyIdentifier(safetyIdentifier string) llms.CallOption {
	return func(opts *llms.CallOptions) {
		requestOptionsFor(opts).SafetyIdentifier = safetyIdentifier
	}
}

func WithDeveloperRole(enabled bool) llms.CallOption {
	return func(opts *llms.CallOptions) {
		requestOptionsFor(opts).DeveloperRole = enabled
	}
}

func WithTemperature(temperature float64) llms.CallOption {
	return func(opts *llms.CallOptions) {
		opts.Temperature = temperature
		requestOptionsFor(opts).Temperature = &temperature
	}
}

func requestOptionsFor(opts *llms.CallOptions) *requestOptions {
	if opts.Metadata == nil {
		opts.Metadata = make(map[string]any)
	}
	if existing, ok := opts.Metadata[requestOptionsMetadataKey].(*requestOptions); ok {
		return existing
	}
	requestOptions := &requestOptions{}
	opts.Metadata[requestOptionsMetadataKey] = requestOptions
	return requestOptions
}

func requestOptionsFrom(opts *llms.CallOptions) requestOptions {
	if opts == nil || opts.Metadata == nil {
		return requestOptions{}
	}
	configured, _ := opts.Metadata[requestOptionsMetadataKey].(*requestOptions)
	if configured == nil {
		return requestOptions{}
	}
	cloned := *configured
	cloned.ExtraBody = cloneAnyMap(configured.ExtraBody)
	return cloned
}
