package openaiclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

const (
	defaultBaseURL = "https://api.openai.com/v1"
)

// ErrEmptyResponse is returned when the OpenAI API returns an empty response.
var ErrEmptyResponse = errors.New("empty response")

type errorMessage struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error"`
	Message string `json:"message"`
	Detail  any    `json:"detail"`
}

type APIType string

const (
	APITypeOpenAI  APIType = "OPEN_AI"
	APITypeAzure   APIType = "AZURE"
	APITypeAzureAD APIType = "AZURE_AD"
)

// Client is a client for the OpenAI API.
type Client struct {
	token        string
	Model        string
	baseURL      string
	organization string
	apiType      APIType
	httpClient   Doer
	Provider     string
	ExtraBody    map[string]any
	ExtraHeaders map[string]string

	EmbeddingModel      string
	EmbeddingDimensions int
	// required when APIType is APITypeAzure or APITypeAzureAD
	apiVersion string

	ResponseFormat *ResponseFormat
}

// Option is an option for the OpenAI client.
type Option func(*Client) error

// WithEmbeddingDimensions allows to setup specific dimensions for embedding's vector
func WithEmbeddingDimensions(dimensions int) Option {
	return func(c *Client) error {
		c.EmbeddingDimensions = dimensions
		return nil
	}
}

// Doer performs a HTTP request.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// New returns a new OpenAI client.
func New(token string, model string, baseURL string, organization string,
	apiType APIType, apiVersion string, httpClient Doer, embeddingModel string,
	responseFormat *ResponseFormat, provider string, extraBody map[string]any, extraHeaders map[string]string,
	opts ...Option,
) (*Client, error) {
	c := &Client{
		token:          token,
		Model:          model,
		EmbeddingModel: embeddingModel,
		baseURL:        strings.TrimSuffix(baseURL, "/"),
		organization:   organization,
		apiType:        apiType,
		apiVersion:     apiVersion,
		httpClient:     httpClient,
		ResponseFormat: responseFormat,
		Provider:       provider,
		ExtraBody:      cloneAnyMap(extraBody),
		ExtraHeaders:   cloneStringMap(extraHeaders),
	}
	if c.baseURL == "" {
		c.baseURL = defaultBaseURL
	}

	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}

	return c, nil
}

// EmbeddingRequest is a request to create an embedding.
type EmbeddingRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions"`
}

func (c *Client) makeEmbeddingPayload(r *EmbeddingRequest) *embeddingPayload {
	payload := &embeddingPayload{
		Model:      c.EmbeddingModel,
		Dimensions: c.EmbeddingDimensions,
		Input:      r.Input,
	}
	if r.Model != "" {
		payload.Model = r.Model
	}
	if payload.Model == "" {
		payload.Model = defaultEmbeddingModel
	}
	if r.Dimensions > 0 {
		payload.Dimensions = r.Dimensions
	}
	return payload
}

// CreateEmbedding creates embeddings.
func (c *Client) CreateEmbedding(ctx context.Context, r *EmbeddingRequest) ([][]float32, error) {
	if r.Model == "" {
		r.Model = defaultEmbeddingModel
	}

	resp, err := c.createEmbedding(ctx, c.makeEmbeddingPayload(r))
	if err != nil {
		return nil, err
	}

	if len(resp.Data) == 0 {
		return nil, ErrEmptyResponse
	}

	embeddings := make([][]float32, 0)
	for i := 0; i < len(resp.Data); i++ {
		embeddings = append(embeddings, resp.Data[i].Embedding)
	}

	return embeddings, nil
}

// CreateChat creates chat request.
func (c *Client) CreateChat(ctx context.Context, r *ChatRequest) (*ChatCompletionResponse, error) {
	if r.Model == "" {
		if c.Model == "" {
			r.Model = defaultChatModel
		} else {
			r.Model = c.Model
		}
	}
	resp, err := c.createChat(ctx, r)
	if err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return nil, ErrEmptyResponse
	}
	return resp, nil
}

// CreateCompletion creates a raw text completion request.
func (c *Client) CreateCompletion(ctx context.Context, r *CompletionRequest) (*CompletionResponse, error) {
	if r.Model == "" {
		if c.Model == "" {
			r.Model = defaultChatModel
		} else {
			r.Model = c.Model
		}
	}
	resp, err := c.createCompletion(ctx, r)
	if err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return nil, ErrEmptyResponse
	}
	return resp, nil
}

func IsAzure(apiType APIType) bool {
	return apiType == APITypeAzure || apiType == APITypeAzureAD
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if c.apiType == APITypeOpenAI || c.apiType == APITypeAzureAD {
		req.Header.Set("Authorization", "Bearer "+c.token)
	} else {
		req.Header.Set("api-key", c.token)
	}
	if c.organization != "" {
		req.Header.Set("OpenAI-Organization", c.organization)
	}
	for name, value := range c.ExtraHeaders {
		name = strings.TrimSpace(name)
		if name != "" {
			req.Header.Set(name, value)
		}
	}
}

func (c *Client) buildURL(suffix string, model string) string {
	if IsAzure(c.apiType) && !c.isAzureV1() {
		return c.buildAzureURL(suffix, model)
	}

	// open ai implement:
	return fmt.Sprintf("%s%s", c.baseURL, suffix)
}

func (c *Client) isAzureV1() bool {
	baseURL := strings.ToLower(strings.TrimRight(c.baseURL, "/"))
	return strings.HasSuffix(baseURL, "/openai/v1")
}

func (c *Client) buildAzureURL(suffix string, model string) string {
	baseURL := c.baseURL
	baseURL = strings.TrimRight(baseURL, "/")

	// azure example url:
	// /openai/deployments/{model}/chat/completions?api-version={api_version}
	return fmt.Sprintf("%s/openai/deployments/%s%s?api-version=%s",
		baseURL, model, suffix, c.apiVersion,
	)
}

// sanitizeHTTPError sanitizes HTTP client errors to prevent leaking sensitive information.
// It checks for context deadline/cancellation errors and returns generic timeout messages
// instead of potentially exposing request details, headers, or other sensitive data.
func sanitizeHTTPError(err error) error {
	if err == nil {
		return nil
	}

	// Check for context deadline exceeded
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("request timeout: API call exceeded deadline")
	}

	// Check for context cancellation
	if errors.Is(err, context.Canceled) {
		return errors.New("request cancelled")
	}

	// Check for network timeout errors
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return errors.New("request timeout: network operation exceeded timeout")
	}

	// For other network errors, provide generic message without exposing details
	if _, ok := err.(net.Error); ok {
		return errors.New("network error: failed to reach API server")
	}

	// Return original error if it's not a sensitive type
	return err
}

func decodeHTTPStatusError(statusCode int, body io.Reader) error {
	message := fmt.Sprintf("API returned unexpected status code: %d", statusCode)
	payload, err := io.ReadAll(io.LimitReader(body, 1<<20))
	if err != nil || len(payload) == 0 {
		return errors.New(message)
	}

	var response errorMessage
	if err := json.Unmarshal(payload, &response); err == nil {
		providerMessage := strings.TrimSpace(response.Error.Message)
		if providerMessage == "" {
			providerMessage = strings.TrimSpace(response.Message)
		}
		if providerMessage == "" && response.Detail != nil {
			if detail, ok := response.Detail.(string); ok {
				providerMessage = strings.TrimSpace(detail)
			} else if encoded, marshalErr := json.Marshal(response.Detail); marshalErr == nil {
				providerMessage = string(encoded)
			}
		}
		if providerMessage != "" {
			return fmt.Errorf("%s: %s", message, providerMessage)
		}
	}

	plainText := strings.TrimSpace(string(payload))
	if len(plainText) > 512 {
		plainText = plainText[:512]
	}
	if plainText == "" {
		return errors.New(message)
	}
	return fmt.Errorf("%s: %s", message, plainText)
}

func mergeExtraBodyFields(payload []byte, extraBody map[string]any, requestName string) ([]byte, error) {
	if len(extraBody) == 0 {
		return payload, nil
	}
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		return nil, err
	}
	for key, value := range extraBody {
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("extra body field %q conflicts with %s", key, requestName)
		}
		fields[key] = value
	}
	return json.Marshal(fields)
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
