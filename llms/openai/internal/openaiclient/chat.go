package openaiclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/dengzii/weaveflow/llms"
)

const (
	defaultChatModel = "gpt-3.5-turbo"
)

var ErrContentExclusive = errors.New("only one of Content / MultiContent allowed in message")

type StreamOptions struct {
	// If set, an additional chunk will be streamed before the data: [DONE] message.
	// The usage field on this chunk shows the token usage statistics for the entire request,
	// and the choices field will always be an empty array.
	// All other chunks will also include a usage field, but with a null value.
	IncludeUsage bool `json:"include_usage,omitempty"`
}

// ChatRequest is a request to complete a chat completion..
type ChatRequest struct {
	Model               string         `json:"model"`
	Messages            []*ChatMessage `json:"messages"`
	Temperature         *float64       `json:"temperature,omitempty"`
	TopP                *float64       `json:"top_p,omitempty"`
	MaxTokens           *int           `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int           `json:"max_completion_tokens,omitempty"`
	N                   int            `json:"n,omitempty"`
	StopWords           []string       `json:"stop,omitempty"`
	Stream              bool           `json:"stream,omitempty"`
	FrequencyPenalty    *float64       `json:"frequency_penalty,omitempty"`
	PresencePenalty     *float64       `json:"presence_penalty,omitempty"`
	Seed                *int           `json:"seed,omitempty"`
	RandomSeed          *int           `json:"random_seed,omitempty"`

	// ResponseFormat is the format of the response.
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`

	// LogProbs indicates whether to return log probabilities of the output tokens or not.
	// If true, returns the log probabilities of each output token returned in the content of message.
	// This option is currently not available on the gpt-4-vision-preview model.
	LogProbs bool `json:"logprobs,omitempty"`
	// TopLogProbs is an integer between 0 and 5 specifying the number of most likely tokens to return at each
	// token position, each with an associated log probability.
	// logprobs must be set to true if this parameter is used.
	TopLogProbs int `json:"top_logprobs,omitempty"`

	Tools []Tool `json:"tools,omitempty"`
	// This can be either a string or a ToolChoice object.
	// If it is a string, it should be one of 'none', or 'auto', otherwise it should be a ToolChoice object specifying a specific tool to use.
	ToolChoice        any   `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool `json:"parallel_tool_calls,omitempty"`

	// Options for streaming response. Only set this when you set stream: true.
	StreamOptions *StreamOptions `json:"stream_options,omitempty"`

	// ReasoningEffort controls thinking effort for reasoning models.
	ReasoningEffort  string `json:"reasoning_effort,omitempty"`
	ServiceTier      string `json:"service_tier,omitempty"`
	Store            *bool  `json:"store,omitempty"`
	Verbosity        string `json:"verbosity,omitempty"`
	PromptCacheKey   string `json:"prompt_cache_key,omitempty"`
	SafetyIdentifier string `json:"safety_identifier,omitempty"`

	// StreamingFunc is a function to be called for each chunk of a streaming response.
	// Return an error to stop streaming early.
	StreamingFunc func(ctx context.Context, chunk []byte) error `json:"-"`

	// StreamingReasoningFunc is a function to be called for each reasoning and content chunk of a streaming response.
	// Return an error to stop streaming early.
	StreamingReasoningFunc func(ctx context.Context, reasoningChunk, chunk []byte) error `json:"-"`

	// Metadata allows you to specify additional information that will be passed to the model.
	Metadata  map[string]any `json:"metadata,omitempty"`
	ExtraBody map[string]any `json:"-"`
}

func (r ChatRequest) MarshalJSON() ([]byte, error) {
	type Alias ChatRequest
	payload, err := json.Marshal(Alias(r))
	if err != nil {
		return nil, err
	}
	return mergeExtraBodyFields(payload, r.ExtraBody, "chat request")
}

// ToolType is the type of a tool.
type ToolType string

const (
	ToolTypeFunction ToolType = "function"
)

// Tool is a tool to use in a chat request.
type Tool struct {
	Type     ToolType           `json:"type"`
	Function FunctionDefinition `json:"function,omitempty"`
}

// ToolChoice is a choice of a tool to use.
type ToolChoice struct {
	Type     ToolType     `json:"type"`
	Function ToolFunction `json:"function,omitempty"`
}

// ToolFunction is a function to be called in a tool choice.
type ToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolCall is a call to a tool.
type ToolCall struct {
	Index    *int         `json:"index,omitempty"`
	ID       string       `json:"id,omitempty"`
	Type     ToolType     `json:"type"`
	Function ToolFunction `json:"function,omitempty"`
}

type ResponseFormatJSONSchemaProperty struct {
	Type                 string                                       `json:"type"`
	Description          string                                       `json:"description,omitempty"`
	Enum                 []interface{}                                `json:"enum,omitempty"`
	Items                *ResponseFormatJSONSchemaProperty            `json:"items,omitempty"`
	Properties           map[string]*ResponseFormatJSONSchemaProperty `json:"properties,omitempty"`
	AdditionalProperties bool                                         `json:"additionalProperties"`
	Required             []string                                     `json:"required,omitempty"`
	Ref                  string                                       `json:"$ref,omitempty"`
}

type ResponseFormatJSONSchema struct {
	Name   string `json:"name"`
	Strict bool   `json:"strict"`
	Schema any    `json:"schema"`
}

// ResponseFormat is the format of the response.
type ResponseFormat struct {
	Type       string                    `json:"type"`
	JSONSchema *ResponseFormatJSONSchema `json:"json_schema,omitempty"`
}

// ChatMessage is a message in a chat request.
type ChatMessage struct { //nolint:musttag
	// The role of the author of this message. One of system, user, assistant, or tool.
	Role string

	// The content of the message.
	// This field is mutually exclusive with MultiContent.
	Content string

	// MultiContent is a list of content parts to use in the message.
	MultiContent []llms.ContentPart

	// The name of the author of this message. May contain a-z, A-Z, 0-9, and underscores,
	// with a maximum length of 64 characters.
	Name string

	// ToolCalls is a list of tools that were called in the message.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`

	// ToolCallID is the ID of the tool call this message is for.
	// Only present in tool messages.
	ToolCallID string `json:"tool_call_id,omitempty"`

	// This field is only used with the deepseek-reasoner model and represents the reasoning contents of the assistant message before the final answer.
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

func (m ChatMessage) MarshalJSON() ([]byte, error) {
	if m.Content != "" && m.MultiContent != nil {
		return nil, ErrContentExclusive
	}
	if text, ok := isSingleTextContent(m.MultiContent); ok {
		m.Content = text
		m.MultiContent = nil
	}
	if len(m.MultiContent) > 0 {
		msg := struct {
			Role         string             `json:"role"`
			Content      string             `json:"-"`
			MultiContent []llms.ContentPart `json:"content,omitempty"`
			Name         string             `json:"name,omitempty"`
			ToolCalls    []ToolCall         `json:"tool_calls,omitempty"`

			// ToolCallID is the ID of the tool call this message is for.
			// Only present in tool messages.
			ToolCallID string `json:"tool_call_id,omitempty"`

			// This field is only used with the deepseek-reasoner model and represents the reasoning contents of the assistant message before the final answer.
			ReasoningContent string `json:"reasoning_content,omitempty"`
		}(m)
		return json.Marshal(msg)
	}
	msg := struct {
		Role         string             `json:"role"`
		Content      string             `json:"content"`
		MultiContent []llms.ContentPart `json:"-"`
		Name         string             `json:"name,omitempty"`
		ToolCalls    []ToolCall         `json:"tool_calls,omitempty"`

		// ToolCallID is the ID of the tool call this message is for.
		// Only present in tool messages.
		ToolCallID string `json:"tool_call_id,omitempty"`

		// This field is only used with the deepseek-reasoner model and represents the reasoning contents of the assistant message before the final answer.
		ReasoningContent string `json:"reasoning_content,omitempty"`
	}(m)
	return json.Marshal(msg)
}

func isSingleTextContent(parts []llms.ContentPart) (string, bool) {
	if len(parts) != 1 {
		return "", false
	}
	tc, isText := parts[0].(llms.TextContent)
	return tc.Text, isText
}

func (m *ChatMessage) UnmarshalJSON(data []byte) error {
	msg := struct {
		Role         string             `json:"role"`
		Content      string             `json:"content"`
		MultiContent []llms.ContentPart `json:"-"` // not expected in response
		Name         string             `json:"name,omitempty"`
		ToolCalls    []ToolCall         `json:"tool_calls,omitempty"`

		// ToolCallID is the ID of the tool call this message is for.
		// Only present in tool messages.
		ToolCallID string `json:"tool_call_id,omitempty"`

		// This field is only used with the deepseek-reasoner model and represents the reasoning contents of the assistant message before the final answer.
		ReasoningContent string `json:"reasoning_content,omitempty"`
	}{}
	err := json.Unmarshal(data, &msg)
	if err != nil {
		return err
	}
	*m = ChatMessage(msg)
	return nil
}

type TopLogProbs struct {
	Token   string  `json:"token"`
	LogProb float64 `json:"logprob"`
	Bytes   []byte  `json:"bytes,omitempty"`
}

// LogProb represents the probability information for a token.
type LogProb struct {
	Token   string  `json:"token"`
	LogProb float64 `json:"logprob"`
	Bytes   []byte  `json:"bytes,omitempty"` // Omitting the field if it is null
	// TopLogProbs is a list of the most likely tokens and their log probability, at this token position.
	// In rare cases, there may be fewer than the number of requested top_logprobs returned.
	TopLogProbs []TopLogProbs `json:"top_logprobs"`
}

// LogProbs is the top-level structure containing the log probability information.
type LogProbs struct {
	// Content is a list of message content tokens with log probability information.
	Content []LogProb `json:"content"`
}

type FinishReason string

const (
	FinishReasonStop          FinishReason = "stop"
	FinishReasonLength        FinishReason = "length"
	FinishReasonToolCalls     FinishReason = "tool_calls"
	FinishReasonContentFilter FinishReason = "content_filter"
	FinishReasonNull          FinishReason = "null"
)

func (r FinishReason) MarshalJSON() ([]byte, error) {
	if r == FinishReasonNull || r == "" {
		return []byte("null"), nil
	}
	return []byte(`"` + string(r) + `"`), nil // best effort to not break future API changes
}

// ChatCompletionChoice is a choice in a chat response.
type ChatCompletionChoice struct {
	Index        int          `json:"index"`
	Message      ChatMessage  `json:"message"`
	FinishReason FinishReason `json:"finish_reason"`
	LogProbs     *LogProbs    `json:"logprobs,omitempty"`
}

// ChatUsage is the usage of a chat completion request.
type ChatUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
		AudioTokens  int `json:"audio_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails struct {
		ReasoningTokens          int `json:"reasoning_tokens"`
		AudioTokens              int `json:"audio_tokens"`
		AcceptedPredictionTokens int `json:"accepted_prediction_tokens"`
		RejectedPredictionTokens int `json:"rejected_prediction_tokens"`
	} `json:"completion_tokens_details"`
}

// ChatCompletionResponse is a response to a chat request.
type ChatCompletionResponse struct {
	ID                string                  `json:"id,omitempty"`
	Created           int64                   `json:"created,omitempty"`
	Choices           []*ChatCompletionChoice `json:"choices,omitempty"`
	Model             string                  `json:"model,omitempty"`
	Object            string                  `json:"object,omitempty"`
	Usage             ChatUsage               `json:"usage,omitempty"`
	SystemFingerprint string                  `json:"system_fingerprint"`
}

type Usage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
		AudioTokens  int `json:"audio_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails struct {
		ReasoningTokens          int `json:"reasoning_tokens"`
		AudioTokens              int `json:"audio_tokens"`
		AcceptedPredictionTokens int `json:"accepted_prediction_tokens"`
		RejectedPredictionTokens int `json:"rejected_prediction_tokens"`
	} `json:"completion_tokens_details"`
}

// StreamedChatResponsePayload is a chunk from the stream.
type StreamedChatResponsePayload struct {
	ID      string  `json:"id,omitempty"`
	Created float64 `json:"created,omitempty"`
	Model   string  `json:"model,omitempty"`
	Object  string  `json:"object,omitempty"`
	Choices []struct {
		Index int `json:"index,omitempty"`
		Delta struct {
			Role    string `json:"role,omitempty"`
			Content string `json:"content,omitempty"`
			// ToolCalls is a list of tools that were called in the message.
			ToolCalls []*ToolCall `json:"tool_calls,omitempty"`
			// This field is only used with the deepseek-reasoner model and represents the reasoning contents of the assistant message before the final answer.
			ReasoningContent string `json:"reasoning_content,omitempty"`
		} `json:"delta,omitempty"`
		FinishReason FinishReason `json:"finish_reason,omitempty"`
	} `json:"choices,omitempty"`
	SystemFingerprint string `json:"system_fingerprint"`
	// An optional field that will only be present when you set stream_options: {"include_usage": true} in your request.
	// When present, it contains a null value except for the last chunk which contains the token usage statistics
	// for the entire request.
	Usage       *Usage         `json:"usage,omitempty"`
	APIError    *ResponseError `json:"error,omitempty"`
	StreamError error          `json:"-"`
}

// FunctionDefinition is a definition of a function that can be called by the model.
type FunctionDefinition struct {
	// Name is the name of the function.
	Name string `json:"name"`
	// Description is a description of the function.
	Description string `json:"description,omitempty"`
	// Parameters is a list of parameters for the function.
	Parameters any `json:"parameters"`
	// Strict is a flag to enable structured output mode.
	Strict bool `json:"strict,omitempty"`
}

// FunctionCall is a call to a function.
type FunctionCall struct {
	// Name is the name of the function to call.
	Name string `json:"name"`
	// Arguments is the set of arguments to pass to the function.
	Arguments string `json:"arguments"`
}

func (c *Client) createChat(ctx context.Context, payload *ChatRequest) (*ChatCompletionResponse, error) {
	if payload.StreamingFunc != nil || payload.StreamingReasoningFunc != nil {
		payload.Stream = true
		if payload.StreamOptions == nil {
			payload.StreamOptions = &StreamOptions{IncludeUsage: true}
		}
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	// Build request
	body := bytes.NewReader(payloadBytes)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.buildURL("/chat/completions", payload.Model), body)
	if err != nil {
		return nil, err
	}

	c.setHeaders(req)

	// Send request
	r, err := c.httpClient.Do(req)
	if err != nil {
		return nil, sanitizeHTTPError(err)
	}
	defer func() { _ = r.Body.Close() }()

	if r.StatusCode != http.StatusOK {
		return nil, c.decodeHTTPStatusError(r)
	}
	if payload.StreamingFunc != nil || payload.StreamingReasoningFunc != nil {
		return parseStreamingChatResponse(ctx, r, payload)
	}
	// Parse response
	var response ChatCompletionResponse
	return &response, json.NewDecoder(r.Body).Decode(&response)
}

func parseStreamingChatResponse(ctx context.Context, r *http.Response, payload *ChatRequest) (*ChatCompletionResponse,
	error,
) { //nolint:cyclop,lll
	scanner := bufio.NewScanner(r.Body)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	responseChan := make(chan StreamedChatResponsePayload)

	// Create a context that can be cancelled to stop the goroutine
	readerCtx, cancelReader := context.WithCancel(ctx)
	defer cancelReader()

	go func() {
		defer close(responseChan)
		for scanner.Scan() {
			// Check if context is cancelled
			select {
			case <-readerCtx.Done():
				return
			default:
			}

			line := scanner.Text()
			if line == "" {
				continue
			}

			// Skip SSE comment lines (any line starting with ':')
			// According to SSE spec: https://www.w3.org/TR/eventsource/
			// "Lines that start with a U+003A COLON character (:) are comments"
			if strings.HasPrefix(line, ":") {
				continue
			}

			// Only process lines that start with "data:"
			if !strings.HasPrefix(line, "data:") {
				// Skip any other non-data lines (like event:, id:, retry:, etc.)
				continue
			}

			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			data = strings.TrimSpace(data)
			if data == "[DONE]" {
				return
			}
			var streamPayload StreamedChatResponsePayload
			err := json.NewDecoder(bytes.NewReader([]byte(data))).Decode(&streamPayload)
			if err != nil {
				if len(data) > 256 {
					data = data[:256]
				}
				select {
				case <-readerCtx.Done():
					return
				case responseChan <- StreamedChatResponsePayload{StreamError: fmt.Errorf("decode streaming response %q: %w", data, err)}:
				}
				return
			}
			if streamPayload.APIError != nil {
				select {
				case <-readerCtx.Done():
					return
				case responseChan <- streamPayload:
				}
				return
			}

			// Non-blocking send with context check
			select {
			case <-readerCtx.Done():
				return
			case responseChan <- streamPayload:
			}
		}
		if err := scanner.Err(); err != nil {
			select {
			case <-readerCtx.Done():
				return
			case responseChan <- StreamedChatResponsePayload{StreamError: fmt.Errorf("error reading streaming response: %w", err)}:
			}
			return
		}
	}()
	// Combine response
	return combineStreamingChatResponse(readerCtx, payload, responseChan)
}

func combineStreamingChatResponse(
	ctx context.Context,
	payload *ChatRequest,
	responseChan chan StreamedChatResponsePayload,
) (*ChatCompletionResponse, error) {
	response := ChatCompletionResponse{}
	choicesByIndex := map[int]*ChatCompletionChoice{}

	for streamResponse := range responseChan {
		if streamResponse.StreamError != nil {
			return nil, streamResponse.StreamError
		}
		if streamResponse.APIError != nil {
			return nil, fmt.Errorf("streaming API error %v: %s", streamResponse.APIError.Code, streamResponse.APIError.Message)
		}
		if streamResponse.ID != "" {
			response.ID = streamResponse.ID
		}
		if streamResponse.Model != "" {
			response.Model = streamResponse.Model
		}
		if streamResponse.Object != "" {
			response.Object = streamResponse.Object
		}
		if streamResponse.Created != 0 {
			response.Created = int64(streamResponse.Created)
		}
		if streamResponse.SystemFingerprint != "" {
			response.SystemFingerprint = streamResponse.SystemFingerprint
		}

		if streamResponse.Usage != nil {
			response.Usage.CompletionTokens = streamResponse.Usage.CompletionTokens
			response.Usage.PromptTokens = streamResponse.Usage.PromptTokens
			response.Usage.TotalTokens = streamResponse.Usage.TotalTokens
			response.Usage.PromptTokensDetails.AudioTokens = streamResponse.Usage.PromptTokensDetails.AudioTokens
			response.Usage.PromptTokensDetails.CachedTokens = streamResponse.Usage.PromptTokensDetails.CachedTokens
			response.Usage.CompletionTokensDetails.AudioTokens = streamResponse.Usage.CompletionTokensDetails.AudioTokens
			response.Usage.CompletionTokensDetails.AcceptedPredictionTokens = streamResponse.Usage.CompletionTokensDetails.AcceptedPredictionTokens
			response.Usage.CompletionTokensDetails.RejectedPredictionTokens = streamResponse.Usage.CompletionTokensDetails.RejectedPredictionTokens
			response.Usage.CompletionTokensDetails.ReasoningTokens = streamResponse.Usage.CompletionTokensDetails.ReasoningTokens
		}

		if len(streamResponse.Choices) == 0 {
			continue
		}
		for _, choice := range streamResponse.Choices {
			choiceIndex := choice.Index
			combined := choicesByIndex[choiceIndex]
			if combined == nil {
				combined = &ChatCompletionChoice{Index: choiceIndex}
				choicesByIndex[choiceIndex] = combined
			}
			chunk := []byte(choice.Delta.Content)
			reasoningChunk := []byte(choice.Delta.ReasoningContent)
			combined.Message.Content += choice.Delta.Content
			combined.Message.ReasoningContent += choice.Delta.ReasoningContent
			if choice.Delta.Role != "" {
				combined.Message.Role = choice.Delta.Role
			}
			if choice.FinishReason != "" {
				combined.FinishReason = choice.FinishReason
			}
			if len(choice.Delta.ToolCalls) > 0 {
				chunk, combined.Message.ToolCalls = updateToolCalls(combined.Message.ToolCalls, choice.Delta.ToolCalls)
			}
			if choiceIndex != 0 {
				continue
			}
			if payload.StreamingFunc != nil {
				if err := payload.StreamingFunc(ctx, chunk); err != nil {
					return nil, fmt.Errorf("streaming func returned an error: %w", err)
				}
			}
			if payload.StreamingReasoningFunc != nil {
				if err := payload.StreamingReasoningFunc(ctx, reasoningChunk, chunk); err != nil {
					return nil, fmt.Errorf("streaming reasoning func returned an error: %w", err)
				}
			}
		}
	}
	response.Choices = make([]*ChatCompletionChoice, 0, len(choicesByIndex))
	for _, choice := range choicesByIndex {
		response.Choices = append(response.Choices, choice)
	}
	sort.Slice(response.Choices, func(left, right int) bool {
		return response.Choices[left].Index < response.Choices[right].Index
	})
	return &response, nil
}

func updateToolCalls(tools []ToolCall, delta []*ToolCall) ([]byte, []ToolCall) {
	if len(delta) == 0 {
		return []byte{}, tools
	}
	for _, toolDelta := range delta {
		if toolDelta == nil {
			continue
		}
		toolIndex := len(tools) - 1
		if toolDelta.Index != nil {
			toolIndex = *toolDelta.Index
		} else if toolDelta.ID != "" || toolDelta.Type != "" || toolDelta.Function.Name != "" {
			toolIndex = len(tools)
		}
		if toolIndex < 0 {
			continue
		}
		for len(tools) <= toolIndex {
			tools = append(tools, ToolCall{})
		}
		target := &tools[toolIndex]
		index := toolIndex
		target.Index = &index
		if toolDelta.ID != "" {
			target.ID = toolDelta.ID
		}
		if toolDelta.Type != "" {
			target.Type = toolDelta.Type
		}
		if toolDelta.Function.Name != "" {
			target.Function.Name = toolDelta.Function.Name
		}
		target.Function.Arguments += toolDelta.Function.Arguments
	}

	chunk, _ := json.Marshal(delta) // nolint:errchkjson

	return chunk, tools
}

// StreamingChatResponseTools is a helper function to append tool calls to the stack.
func StreamingChatResponseTools(tools []ToolCall, delta []*ToolCall) ([]byte, []ToolCall) {
	return updateToolCalls(tools, delta)
}
