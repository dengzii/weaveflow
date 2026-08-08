package openai

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/dengzii/weaveflow/llms/openai/internal/openaiclient"

	"github.com/tmc/langchaingo/httputil"
)

var (
	ErrEmptyResponse     = errors.New("no response")
	ErrMissingToken      = errors.New("missing the OpenAI API key, set it in the OPENAI_API_KEY environment variable") //nolint:lll
	ErrMissingAzureModel = errors.New("model needs to be provided when using Azure API")
	ErrMissingBaseURL    = errors.New("base URL is required for this OpenAI-compatible provider")

	ErrUnexpectedResponseLength = errors.New("unexpected length of response")
)

// newClient creates an instance of the internal client.
func newClient(opts ...Option) (*options, *openaiclient.Client, error) {
	options := &options{
		token:        os.Getenv(tokenEnvVarName),
		model:        os.Getenv(modelEnvVarName),
		baseURL:      os.Getenv(baseURLEnvVarName),
		organization: os.Getenv(organizationEnvVarName),
		apiType:      APIType(openaiclient.APITypeOpenAI),
		provider:     ProviderOpenAI,
		apiFormat:    APIFormatChatCompletions,
		httpClient:   httputil.DefaultClient,
	}

	for _, opt := range opts {
		opt(options)
	}
	if !IsSupportedProvider(options.provider) {
		return options, nil, fmt.Errorf("unsupported OpenAI-compatible provider %q", options.provider)
	}
	if !IsSupportedAPIFormat(options.apiFormat) {
		return options, nil, fmt.Errorf("unsupported OpenAI API format %q", options.apiFormat)
	}
	if options.provider == ProviderAzure && !openaiclient.IsAzure(openaiclient.APIType(options.apiType)) {
		options.apiType = APITypeAzure
	}
	if openaiclient.IsAzure(openaiclient.APIType(options.apiType)) {
		if options.provider == ProviderOpenAI {
			options.provider = ProviderAzure
		}
		if options.apiVersion == "" {
			options.apiVersion = DefaultAPIVersion
		}
		if strings.TrimSpace(options.model) == "" {
			return options, nil, ErrMissingAzureModel
		}
	}
	if strings.TrimSpace(options.baseURL) == "" {
		options.baseURL = defaultBaseURLForProvider(options.provider)
		if options.baseURL == "" {
			return options, nil, ErrMissingBaseURL
		}
	}

	if len(options.token) == 0 {
		return options, nil, ErrMissingToken
	}

	var clientOptions []openaiclient.Option
	if options.embeddingDimensions != 0 {
		clientOptions = append(clientOptions, openaiclient.WithEmbeddingDimensions(options.embeddingDimensions))
	}
	cli, err := openaiclient.New(options.token, options.model, options.baseURL, options.organization,
		openaiclient.APIType(options.apiType), options.apiVersion, options.httpClient, options.embeddingModel,
		options.responseFormat, string(options.provider), options.extraBody, options.extraHeaders, clientOptions...,
	)
	return options, cli, err
}

func defaultBaseURLForProvider(provider Provider) string {
	switch provider {
	case ProviderOpenAI:
		return "https://api.openai.com/v1"
	case ProviderDeepSeek:
		return "https://api.deepseek.com/v1"
	case ProviderGemini:
		return "https://generativelanguage.googleapis.com/v1beta/openai"
	case ProviderMistral:
		return "https://api.mistral.ai/v1"
	case ProviderXAI:
		return "https://api.x.ai/v1"
	case ProviderOpenRouter:
		return "https://openrouter.ai/api/v1"
	default:
		return ""
	}
}
