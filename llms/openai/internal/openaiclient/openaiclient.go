package openaiclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

const (
	defaultBaseURL = "https://api.openai.com/v1"
)

// ErrEmptyResponse is returned when the OpenAI API returns an empty response.
var ErrEmptyResponse = errors.New("empty response")

type errorMessage struct {
	Error struct {
		Message   string `json:"message"`
		Type      string `json:"type"`
		Code      any    `json:"code"`
		Param     any    `json:"param"`
		RequestID string `json:"request_id"`
	} `json:"error"`
	Message   string `json:"message"`
	Detail    any    `json:"detail"`
	RequestID string `json:"request_id"`
}

type HTTPStatusError struct {
	StatusCode      int
	ProviderMessage string
	ProviderType    string
	ProviderCode    string
	ProviderParam   string
	RequestID       string
}

type sanitizedError struct {
	message string
	cause   error
}

func (sanitizedErr *sanitizedError) Error() string {
	if sanitizedErr == nil {
		return "model API request failed"
	}
	return sanitizedErr.message
}

func (sanitizedErr *sanitizedError) Unwrap() error {
	if sanitizedErr == nil {
		return nil
	}
	return sanitizedErr.cause
}

func (statusErr *HTTPStatusError) Error() string {
	if statusErr == nil {
		return "model API request failed"
	}
	message := fmt.Sprintf("model API returned HTTP %d", statusErr.StatusCode)
	if statusText := http.StatusText(statusErr.StatusCode); statusText != "" {
		message += " " + statusText
	}
	if statusErr.ProviderMessage != "" {
		message += ": " + statusErr.ProviderMessage
	}
	attributes := make([]string, 0, 4)
	if statusErr.ProviderType != "" {
		attributes = append(attributes, "type="+statusErr.ProviderType)
	}
	if statusErr.ProviderCode != "" {
		attributes = append(attributes, "code="+statusErr.ProviderCode)
	}
	if statusErr.ProviderParam != "" {
		attributes = append(attributes, "param="+statusErr.ProviderParam)
	}
	if statusErr.RequestID != "" {
		attributes = append(attributes, "request_id="+statusErr.RequestID)
	}
	if len(attributes) > 0 {
		message += " (" + strings.Join(attributes, ", ") + ")"
	}
	return message
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
		return nil, c.sanitizeError(err)
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
		return nil, c.sanitizeError(err)
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
		return nil, c.sanitizeError(err)
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

// sanitizeHTTPError removes the request URL, which may contain credentials, while
// preserving the underlying DNS, TLS, connection, timeout, or cancellation cause.
func sanitizeHTTPError(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("model API request exceeded its deadline: %w", context.DeadlineExceeded)
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("model API request was canceled: %w", context.Canceled)
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		operation := strings.TrimSpace(urlErr.Op)
		if operation == "" {
			operation = "send"
		}
		return fmt.Errorf("model API network request failed during %s: %w", operation, urlErr.Err)
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("model API network request timed out: %w", err)
	}
	if errors.As(err, &netErr) {
		return fmt.Errorf("model API network request failed: %w", err)
	}
	return err
}

func (c *Client) decodeHTTPStatusError(response *http.Response) error {
	statusErr := &HTTPStatusError{}
	if response == nil {
		return statusErr
	}
	statusErr.StatusCode = response.StatusCode
	statusErr.RequestID = c.redactErrorText(responseRequestID(response.Header))
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil || len(payload) == 0 {
		return statusErr
	}

	var payloadError errorMessage
	if err := json.Unmarshal(payload, &payloadError); err == nil {
		providerMessage := strings.TrimSpace(payloadError.Error.Message)
		if providerMessage == "" {
			providerMessage = strings.TrimSpace(payloadError.Message)
		}
		if providerMessage == "" && payloadError.Detail != nil {
			if detail, ok := payloadError.Detail.(string); ok {
				providerMessage = strings.TrimSpace(detail)
			} else if encoded, marshalErr := json.Marshal(payloadError.Detail); marshalErr == nil {
				providerMessage = string(encoded)
			}
		}
		statusErr.ProviderMessage = truncateErrorText(c.redactErrorText(providerMessage), 4096)
		statusErr.ProviderType = c.redactErrorText(strings.TrimSpace(payloadError.Error.Type))
		statusErr.ProviderCode = c.redactErrorText(errorFieldText(payloadError.Error.Code))
		statusErr.ProviderParam = c.redactErrorText(errorFieldText(payloadError.Error.Param))
		if statusErr.RequestID == "" {
			statusErr.RequestID = firstNonEmptyString(payloadError.Error.RequestID, payloadError.RequestID)
		}
		statusErr.RequestID = c.redactErrorText(statusErr.RequestID)
		if statusErr.ProviderMessage != "" || statusErr.ProviderType != "" || statusErr.ProviderCode != "" || statusErr.ProviderParam != "" {
			return statusErr
		}
	}

	statusErr.ProviderMessage = truncateErrorText(c.redactErrorText(string(payload)), 4096)
	return statusErr
}

func (c *Client) redactErrorText(value string) string {
	if c == nil || value == "" {
		return value
	}
	sensitiveValues := []string{c.token, c.organization}
	for _, headerValue := range c.ExtraHeaders {
		sensitiveValues = append(sensitiveValues, headerValue)
	}
	if parsed, err := url.Parse(c.baseURL); err == nil {
		if parsed.User != nil {
			sensitiveValues = append(sensitiveValues, parsed.User.String())
			if password, exists := parsed.User.Password(); exists {
				sensitiveValues = append(sensitiveValues, password)
			}
		}
		for name, values := range parsed.Query() {
			name = strings.ToLower(strings.TrimSpace(name))
			if !strings.Contains(name, "key") && !strings.Contains(name, "token") && !strings.Contains(name, "secret") && !strings.Contains(name, "password") && !strings.Contains(name, "signature") && !strings.Contains(name, "credential") {
				continue
			}
			sensitiveValues = append(sensitiveValues, values...)
		}
	}
	sort.SliceStable(sensitiveValues, func(left, right int) bool {
		return len(sensitiveValues[left]) > len(sensitiveValues[right])
	})
	for _, sensitive := range sensitiveValues {
		sensitive = strings.TrimSpace(sensitive)
		if len(sensitive) < 4 {
			continue
		}
		value = strings.ReplaceAll(value, sensitive, "[REDACTED]")
	}
	return value
}

func (c *Client) sanitizeError(err error) error {
	if err == nil {
		return nil
	}
	message := c.redactErrorText(err.Error())
	if message == err.Error() {
		return err
	}
	return &sanitizedError{message: message, cause: err}
}

func responseRequestID(header http.Header) string {
	for _, name := range []string{"x-request-id", "request-id", "x-ms-request-id", "cf-ray"} {
		if value := strings.TrimSpace(header.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

func errorFieldText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return strings.TrimSpace(fmt.Sprint(typed))
		}
		return strings.TrimSpace(string(encoded))
	}
}

func truncateErrorText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
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
