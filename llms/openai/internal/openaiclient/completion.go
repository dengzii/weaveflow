package openaiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
)

// CompletionRequest is a request to the legacy text completions endpoint.
type CompletionRequest struct {
	Model            string         `json:"model"`
	Prompt           string         `json:"prompt"`
	MaxTokens        int            `json:"max_tokens,omitempty"`
	Temperature      float64        `json:"temperature"`
	TopP             float64        `json:"top_p,omitempty"`
	N                int            `json:"n,omitempty"`
	StopWords        []string       `json:"stop,omitempty"`
	FrequencyPenalty float64        `json:"frequency_penalty,omitempty"`
	PresencePenalty  float64        `json:"presence_penalty,omitempty"`
	Seed             int            `json:"seed,omitempty"`
	ExtraBody        map[string]any `json:"-"`
}

func (r CompletionRequest) MarshalJSON() ([]byte, error) {
	type Alias CompletionRequest
	payload, err := json.Marshal(Alias(r))
	if err != nil {
		return nil, err
	}
	return mergeExtraBodyFields(payload, r.ExtraBody, "completion request")
}

type CompletionChoice struct {
	Text         string `json:"text,omitempty"`
	Index        int    `json:"index,omitempty"`
	FinishReason string `json:"finish_reason,omitempty"`
}

type CompletionResponse struct {
	ID      string              `json:"id,omitempty"`
	Created int64               `json:"created,omitempty"`
	Choices []*CompletionChoice `json:"choices,omitempty"`
	Model   string              `json:"model,omitempty"`
	Object  string              `json:"object,omitempty"`
	Usage   ChatUsage           `json:"usage,omitempty"`
}

func (c *Client) createCompletion(ctx context.Context, payload *CompletionRequest) (*CompletionResponse, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.buildURL("/completions", payload.Model),
		bytes.NewReader(payloadBytes),
	)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)

	response, err := c.httpClient.Do(req)
	if err != nil {
		return nil, sanitizeHTTPError(err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return nil, decodeHTTPStatusError(response.StatusCode, response.Body)
	}

	var result CompletionResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}
