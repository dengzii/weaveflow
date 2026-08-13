package openai

import (
	"github.com/dengzii/weaveflow/llms/openai/internal/openaiclient"
)

const (
	tokenEnvVarName        = "OPENAI_API_KEY"      //nolint:gosec
	modelEnvVarName        = "OPENAI_MODEL"        //nolint:gosec
	baseURLEnvVarName      = "OPENAI_BASE_URL"     //nolint:gosec
	organizationEnvVarName = "OPENAI_ORGANIZATION" //nolint:gosec
)

type APIType openaiclient.APIType

const (
	APITypeOpenAI  APIType = APIType(openaiclient.APITypeOpenAI)
	APITypeAzure           = APIType(openaiclient.APITypeAzure)
	APITypeAzureAD         = APIType(openaiclient.APITypeAzureAD)
)

const (
	DefaultAPIVersion = "2024-10-21"
)

type Provider string

const (
	ProviderOpenAI     Provider = "openai"
	ProviderAzure      Provider = "azure"
	ProviderDeepSeek   Provider = "deepseek"
	ProviderGemini     Provider = "gemini"
	ProviderVLLM       Provider = "vllm"
	ProviderMistral    Provider = "mistral"
	ProviderXAI        Provider = "xai"
	ProviderOpenRouter Provider = "openrouter"
)

type APIFormat string

const (
	APIFormatChatCompletions APIFormat = "chat_completions"
	APIFormatResponses       APIFormat = "responses"
)

type options struct {
	token        string
	model        string
	baseURL      string
	organization string
	apiType      APIType
	provider     Provider
	apiFormat    APIFormat
	httpClient   openaiclient.Doer
	extraBody    map[string]any
	extraHeaders map[string]string

	responseFormat *ResponseFormat

	// required when APIType is APITypeAzure or APITypeAzureAD
	apiVersion          string
	embeddingModel      string
	embeddingDimensions int
}

// Option is a functional option for the OpenAI client.
type Option func(*options)

// ResponseFormat is the response format for the OpenAI client.
type ResponseFormat = openaiclient.ResponseFormat

// ResponseFormatJSONSchema is the JSON Schema response format in structured output.
type ResponseFormatJSONSchema = openaiclient.ResponseFormatJSONSchema

// ResponseFormatJSONSchemaProperty is the JSON Schema property in structured output.
type ResponseFormatJSONSchemaProperty = openaiclient.ResponseFormatJSONSchemaProperty

// ResponseFormatJSON is the JSON response format.
var ResponseFormatJSON = &ResponseFormat{Type: "json_object"} //nolint:gochecknoglobals

// WithToken passes the OpenAI API token to the client. If not set, the token
// is read from the OPENAI_API_KEY environment variable.
func WithToken(token string) Option {
	return func(opts *options) {
		opts.token = token
	}
}

// WithModel passes the OpenAI model to the client. If not set, the model
// is read from the OPENAI_MODEL environment variable.
// Required when ApiType is Azure.
func WithModel(model string) Option {
	return func(opts *options) {
		opts.model = model
	}
}

// WithEmbeddingModel passes the OpenAI model to the client. Required when ApiType is Azure.
func WithEmbeddingModel(embeddingModel string) Option {
	return func(opts *options) {
		opts.embeddingModel = embeddingModel
	}
}

// WithEmbeddingDimensions passes the OpenAI embeddings dimensions to the client.
// Requires a compatible model, test-embedding-3 or later.
// For more info, please check openai doc
// https://platform.openai.com/docs/api-reference/embeddings/create#embeddings-create-dimensions
func WithEmbeddingDimensions(dimensions int) Option {
	return func(opts *options) {
		opts.embeddingDimensions = dimensions
	}
}

// WithBaseURL passes the OpenAI base url to the client. If not set, the base url
// is read from the OPENAI_BASE_URL environment variable. If still not set in ENV
// VAR OPENAI_BASE_URL, then the default value is https://api.openai.com/v1 is used.
func WithBaseURL(baseURL string) Option {
	return func(opts *options) {
		opts.baseURL = baseURL
	}
}

// WithOrganization passes the OpenAI organization to the client. If not set, the
// organization is read from the OPENAI_ORGANIZATION.
func WithOrganization(organization string) Option {
	return func(opts *options) {
		opts.organization = organization
	}
}

// WithAPIType passes the api type to the client. If not set, the default value
// is APITypeOpenAI.
func WithAPIType(apiType APIType) Option {
	return func(opts *options) {
		opts.apiType = apiType
	}
}

func WithProvider(provider Provider) Option {
	return func(opts *options) {
		opts.provider = provider
	}
}

func IsSupportedProvider(provider Provider) bool {
	switch provider {
	case ProviderOpenAI, ProviderAzure, ProviderDeepSeek, ProviderGemini, ProviderVLLM, ProviderMistral, ProviderXAI, ProviderOpenRouter:
		return true
	default:
		return false
	}
}

func WithAPIFormat(apiFormat APIFormat) Option {
	return func(opts *options) {
		opts.apiFormat = apiFormat
	}
}

func IsSupportedAPIFormat(apiFormat APIFormat) bool {
	switch apiFormat {
	case APIFormatChatCompletions, APIFormatResponses:
		return true
	default:
		return false
	}
}

func WithExtraBody(extraBody map[string]any) Option {
	return func(opts *options) {
		opts.extraBody = cloneAnyMap(extraBody)
	}
}

func WithExtraHeaders(extraHeaders map[string]string) Option {
	return func(opts *options) {
		opts.extraHeaders = cloneStringMap(extraHeaders)
	}
}

// WithAPIVersion passes the api version to the client. If not set, the default value
// is DefaultAPIVersion.
func WithAPIVersion(apiVersion string) Option {
	return func(opts *options) {
		opts.apiVersion = apiVersion
	}
}

// WithHTTPClient allows setting a custom HTTP client. If not set, the default value
// is http.DefaultClient.
func WithHTTPClient(client openaiclient.Doer) Option {
	return func(opts *options) {
		opts.httpClient = client
	}
}

// WithResponseFormat allows setting a custom response format.
func WithResponseFormat(responseFormat *ResponseFormat) Option {
	return func(opts *options) {
		opts.responseFormat = responseFormat
	}
}

func cloneAnyMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}
