package openaiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type ResponseRequest struct {
	Model             string              `json:"model"`
	Input             []ResponseInputItem `json:"input"`
	MaxOutputTokens   *int                `json:"max_output_tokens,omitempty"`
	Temperature       *float64            `json:"temperature,omitempty"`
	TopP              *float64            `json:"top_p,omitempty"`
	Tools             []ResponseTool      `json:"tools,omitempty"`
	ToolChoice        any                 `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool               `json:"parallel_tool_calls,omitempty"`
	Reasoning         *ResponseReasoning  `json:"reasoning,omitempty"`
	Text              *ResponseTextConfig `json:"text,omitempty"`
	ServiceTier       string              `json:"service_tier,omitempty"`
	Store             *bool               `json:"store,omitempty"`
	Metadata          map[string]any      `json:"metadata,omitempty"`
	PromptCacheKey    string              `json:"prompt_cache_key,omitempty"`
	SafetyIdentifier  string              `json:"safety_identifier,omitempty"`
	ExtraBody         map[string]any      `json:"-"`
}

func (r ResponseRequest) MarshalJSON() ([]byte, error) {
	type Alias ResponseRequest
	payload, err := json.Marshal(Alias(r))
	if err != nil {
		return nil, err
	}
	return mergeExtraBodyFields(payload, r.ExtraBody, "responses request")
}

type ResponseInputItem struct {
	Type      string            `json:"type,omitempty"`
	Role      string            `json:"role,omitempty"`
	Status    string            `json:"status,omitempty"`
	Content   []ResponseContent `json:"content,omitempty"`
	CallID    string            `json:"call_id,omitempty"`
	Name      string            `json:"name,omitempty"`
	Arguments string            `json:"arguments,omitempty"`
	Output    *string           `json:"output,omitempty"`
}

type ResponseContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

type ResponseTool struct {
	Type        ToolType `json:"type"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Parameters  any      `json:"parameters"`
	Strict      bool     `json:"strict,omitempty"`
}

type ResponseReasoning struct {
	Effort string `json:"effort,omitempty"`
}

type ResponseTextConfig struct {
	Format    any    `json:"format,omitempty"`
	Verbosity string `json:"verbosity,omitempty"`
}

type ResponseTextFormat struct {
	Type   string `json:"type"`
	Name   string `json:"name,omitempty"`
	Strict bool   `json:"strict,omitempty"`
	Schema any    `json:"schema,omitempty"`
}

type Response struct {
	ID                string               `json:"id,omitempty"`
	Model             string               `json:"model,omitempty"`
	Status            string               `json:"status,omitempty"`
	Output            []ResponseOutputItem `json:"output,omitempty"`
	Usage             ResponseUsage        `json:"usage,omitempty"`
	Error             *ResponseError       `json:"error,omitempty"`
	IncompleteDetails *struct {
		Reason string `json:"reason,omitempty"`
	} `json:"incomplete_details,omitempty"`
}

type ResponseOutputItem struct {
	Type      string                  `json:"type,omitempty"`
	Status    string                  `json:"status,omitempty"`
	Role      string                  `json:"role,omitempty"`
	Content   []ResponseOutputContent `json:"content,omitempty"`
	CallID    string                  `json:"call_id,omitempty"`
	Name      string                  `json:"name,omitempty"`
	Arguments string                  `json:"arguments,omitempty"`
	Summary   []struct {
		Type string `json:"type,omitempty"`
		Text string `json:"text,omitempty"`
	} `json:"summary,omitempty"`
}

type ResponseOutputContent struct {
	Type    string `json:"type,omitempty"`
	Text    string `json:"text,omitempty"`
	Refusal string `json:"refusal,omitempty"`
}

type ResponseUsage struct {
	InputTokens        int `json:"input_tokens,omitempty"`
	OutputTokens       int `json:"output_tokens,omitempty"`
	TotalTokens        int `json:"total_tokens,omitempty"`
	InputTokensDetails struct {
		CachedTokens int `json:"cached_tokens,omitempty"`
	} `json:"input_tokens_details,omitempty"`
	OutputTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens,omitempty"`
	} `json:"output_tokens_details,omitempty"`
}

type ResponseError struct {
	Code    any    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

func (c *Client) CreateResponse(ctx context.Context, request *ResponseRequest) (*Response, error) {
	if request.Model == "" {
		if c.Model == "" {
			request.Model = defaultChatModel
		} else {
			request.Model = c.Model
		}
	}
	response, err := c.createResponse(ctx, request)
	if err != nil {
		return nil, err
	}
	if response.Error != nil {
		return nil, fmt.Errorf("Responses API returned %v: %s", response.Error.Code, response.Error.Message)
	}
	return response, nil
}

func (c *Client) createResponse(ctx context.Context, payload *ResponseRequest) (*Response, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.buildURL("/responses", payload.Model),
		bytes.NewReader(payloadBytes),
	)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)

	httpResponse, err := c.httpClient.Do(req)
	if err != nil {
		return nil, sanitizeHTTPError(err)
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode != http.StatusOK {
		return nil, decodeHTTPStatusError(httpResponse.StatusCode, httpResponse.Body)
	}

	var response Response
	if err := json.NewDecoder(httpResponse.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("decode Responses API response: %w", err)
	}
	return &response, nil
}
