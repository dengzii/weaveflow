package openai

import "github.com/dengzii/weaveflow/llms"

const requestOptionsMetadataKey = "weaveflow.openai.request_options"

type RequestOptions struct {
	ExtraBody         map[string]any `json:"extra_body,omitempty"`
	ParallelToolCalls *bool          `json:"parallel_tool_calls,omitempty"`
	ServiceTier       string         `json:"service_tier,omitempty"`
	Store             *bool          `json:"store,omitempty"`
	Verbosity         string         `json:"verbosity,omitempty"`
	PromptCacheKey    string         `json:"prompt_cache_key,omitempty"`
	SafetyIdentifier  string         `json:"safety_identifier,omitempty"`
	DeveloperRole     bool           `json:"developer_role,omitempty"`
}

func ApplyRequestOptions(request *llms.ModelRequest, options RequestOptions) {
	if request == nil {
		return
	}
	if request.ProviderOptions == nil {
		request.ProviderOptions = make(map[string]any)
	}
	options.ExtraBody = cloneAnyMap(options.ExtraBody)
	request.ProviderOptions[requestOptionsMetadataKey] = options
}

func requestOptionsFrom(request llms.ModelRequest) RequestOptions {
	if len(request.ProviderOptions) == 0 {
		return RequestOptions{}
	}
	configured, ok := request.ProviderOptions[requestOptionsMetadataKey].(RequestOptions)
	if !ok {
		if pointer, pointerOK := request.ProviderOptions[requestOptionsMetadataKey].(*RequestOptions); pointerOK && pointer != nil {
			configured = *pointer
		}
	}
	configured.ExtraBody = cloneAnyMap(configured.ExtraBody)
	return configured
}
