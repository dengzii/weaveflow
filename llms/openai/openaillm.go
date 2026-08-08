package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/llms/openai/internal/openaiclient"
	"github.com/dengzii/weaveflow/llms/parts"

	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/llms"
)

type ChatMessage = openaiclient.ChatMessage

type LLM struct {
	CallbacksHandler callbacks.Handler
	client           *openaiclient.Client
	model            string
	provider         Provider
	apiFormat        APIFormat
}

const (
	RoleSystem    = "system"
	RoleDeveloper = "developer"
	RoleAssistant = "assistant"
	RoleUser      = "user"
	RoleTool      = "tool"
)

var (
	_ llms.Model          = (*LLM)(nil)
	_ llms.ReasoningModel = (*LLM)(nil)
)

// New returns a new OpenAI LLM.
func New(opts ...Option) (*LLM, error) {
	opt, c, err := newClient(opts...)
	if err != nil {
		return nil, err
	}
	if !IsSupportedProvider(opt.provider) {
		return nil, fmt.Errorf("unsupported OpenAI-compatible provider %q", opt.provider)
	}
	if !IsSupportedAPIFormat(opt.apiFormat) {
		return nil, fmt.Errorf("unsupported OpenAI API format %q", opt.apiFormat)
	}
	if opt.apiFormat == APIFormatResponses && openaiclient.IsAzure(openaiclient.APIType(opt.apiType)) && !isAzureV1URL(opt.baseURL) {
		return nil, fmt.Errorf("Azure Responses API requires a base URL ending in /openai/v1")
	}
	return &LLM{
		client:           c,
		CallbacksHandler: opt.callbackHandler,
		model:            c.Model,
		provider:         opt.provider,
		apiFormat:        opt.apiFormat,
	}, err
}

func (o *LLM) Name() string {
	return o.client.Model
}

// Call requests a completion for the given prompt.
func (o *LLM) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	return llms.GenerateFromSinglePrompt(ctx, o, prompt, options...)
}

// GenerateCompletion requests a raw text completion without applying chat message templates.
func (o *LLM) GenerateCompletion(ctx context.Context, prompt string, options ...llms.CallOption) (*llms.ContentResponse, error) {
	messages := []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, prompt)}
	opts := llms.CallOptions{}
	for _, option := range options {
		option(&opts)
	}
	if err := validateCompletionCallOptions(opts); err != nil {
		return nil, err
	}
	if thinkingConfig := llms.GetThinkingConfig(&opts); thinkingConfig != nil && thinkingConfig.Mode != "" && thinkingConfig.Mode != llms.ThinkingModeAuto {
		return o.GenerateContent(ctx, messages, options...)
	}
	if o.apiFormat == APIFormatResponses {
		return o.GenerateContent(ctx, messages, options...)
	}
	if o.CallbacksHandler != nil {
		o.CallbacksHandler.HandleLLMGenerateContentStart(ctx, messages)
	}
	requestOptions := requestOptionsFrom(&opts)

	result, err := o.client.CreateCompletion(ctx, &openaiclient.CompletionRequest{
		Model:            opts.Model,
		Prompt:           prompt,
		MaxTokens:        opts.MaxTokens,
		Temperature:      opts.Temperature,
		TopP:             opts.TopP,
		N:                opts.N,
		StopWords:        opts.StopWords,
		FrequencyPenalty: opts.FrequencyPenalty,
		PresencePenalty:  opts.PresencePenalty,
		Seed:             opts.Seed,
		ExtraBody:        mergeExtraBody(o.client.ExtraBody, requestOptions.ExtraBody),
	})
	if err != nil {
		return nil, err
	}

	choices := make([]*llms.ContentChoice, len(result.Choices))
	for index, choice := range result.Choices {
		choices[index] = &llms.ContentChoice{
			Content:    choice.Text,
			StopReason: choice.FinishReason,
			GenerationInfo: map[string]any{
				"CompletionTokens": result.Usage.CompletionTokens,
				"PromptTokens":     result.Usage.PromptTokens,
				"TotalTokens":      result.Usage.TotalTokens,
			},
		}
	}
	response := &llms.ContentResponse{Choices: choices}
	if o.CallbacksHandler != nil {
		o.CallbacksHandler.HandleLLMGenerateContentEnd(ctx, response)
	}
	return response, nil
}

// GenerateContent implements the Model interface.
func (o *LLM) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) { //nolint: lll, cyclop, funlen
	if o.CallbacksHandler != nil {
		o.CallbacksHandler.HandleLLMGenerateContentStart(ctx, messages)
	}

	opts := llms.CallOptions{}
	for _, opt := range options {
		opt(&opts)
	}
	if err := validateCallOptions(opts); err != nil {
		return nil, err
	}
	if o.apiFormat == APIFormatResponses {
		return o.generateResponse(ctx, messages, opts)
	}

	// Determine the effective model for this request (don't mutate o.model to avoid races)
	effectiveModel := opts.Model
	if effectiveModel == "" {
		effectiveModel = o.model
	}

	requestOptions := requestOptionsFrom(&opts)
	useDeveloperRole := requestOptions.DeveloperRole || usesDeveloperRole(o.provider, effectiveModel)

	chatMsgs := make([]*ChatMessage, 0, len(messages))
	for _, mc := range messages {
		msg := &ChatMessage{MultiContent: mc.Parts}
		switch mc.Role {
		case llms.ChatMessageTypeSystem:
			if useDeveloperRole {
				msg.Role = RoleDeveloper
			} else {
				msg.Role = RoleSystem
			}
		case llms.ChatMessageTypeAI:
			msg.Role = RoleAssistant
		case llms.ChatMessageTypeHuman:
			msg.Role = RoleUser
		case llms.ChatMessageTypeGeneric:
			msg.Role = RoleUser
		case llms.ChatMessageTypeTool:
			msg.Role = RoleTool
			// Here we extract tool calls from the message and populate the ToolCalls field.

			// parse mc.Parts (which should have one entry of type ToolCallResponse) and populate msg.Content and msg.ToolCallID
			if len(mc.Parts) != 1 {
				return nil, fmt.Errorf("expected exactly one part for role %v, got %v", mc.Role, len(mc.Parts))
			}
			switch p := mc.Parts[0].(type) {
			case llms.ToolCallResponse:
				msg.ToolCallID = p.ToolCallID
				msg.Content = p.Content
			default:
				return nil, fmt.Errorf("expected part of type ToolCallResponse for role %v, got %T", mc.Role, mc.Parts[0])
			}

		default:
			return nil, fmt.Errorf("role %v not supported", mc.Role)
		}

		// Pull out reasoning parts before tool-call extraction so they end up
		// in the provider-specific reasoning_content channel (DeepSeek-reasoner
		// and similar) rather than bleeding into assistant.content text.
		msg.MultiContent, msg.ReasoningContent = extractReasoningParts(msg.MultiContent)

		// Here we extract tool calls from the message and populate the ToolCalls field.
		newParts, toolCalls := ExtractToolParts(msg)
		msg.MultiContent = newParts
		msg.ToolCalls = toolCallsFromToolCalls(toolCalls)

		chatMsgs = append(chatMsgs, msg)
	}
	// Filter out internal metadata that shouldn't be sent to API
	apiMetadata := make(map[string]any)
	if opts.Metadata != nil {
		for k, v := range opts.Metadata {
			if k == "thinking_config" || k == requestOptionsMetadataKey {
				continue
			}
			apiMetadata[k] = v
		}
	}
	// Only include metadata if there are actual values to send
	if len(apiMetadata) == 0 {
		apiMetadata = nil
	}

	configuredReasoningEffort := reasoningEffort(opts)
	reasoningEffort, providerExtraBody := providerReasoningOptions(o.provider, configuredReasoningEffort)
	extraBody := mergeExtraBody(providerExtraBody, o.client.ExtraBody, requestOptions.ExtraBody)
	req := &openaiclient.ChatRequest{
		Model:                  opts.Model,
		StopWords:              opts.StopWords,
		Messages:               chatMsgs,
		StreamingFunc:          opts.StreamingFunc,
		StreamingReasoningFunc: opts.StreamingReasoningFunc,
		N:                      opts.N,
		FrequencyPenalty:       optionalFloat64(opts.FrequencyPenalty),
		PresencePenalty:        optionalFloat64(opts.PresencePenalty),
		ReasoningEffort:        reasoningEffort,
		ToolChoice:             opts.ToolChoice,
		Metadata:               apiMetadata,
		ParallelToolCalls:      requestOptions.ParallelToolCalls,
		ServiceTier:            strings.TrimSpace(requestOptions.ServiceTier),
		Store:                  requestOptions.Store,
		Verbosity:              strings.TrimSpace(requestOptions.Verbosity),
		PromptCacheKey:         strings.TrimSpace(requestOptions.PromptCacheKey),
		SafetyIdentifier:       strings.TrimSpace(requestOptions.SafetyIdentifier),
		ExtraBody:              extraBody,
	}
	if !omitSamplingParameters(o.provider, effectiveModel, configuredReasoningEffort) {
		if requestOptions.Temperature != nil {
			req.Temperature = requestOptions.Temperature
		} else {
			req.Temperature = optionalFloat64(opts.Temperature)
		}
		req.TopP = optionalFloat64(opts.TopP)
	}
	if opts.MaxTokens > 0 {
		if usesMaxTokens(o.provider) {
			req.MaxTokens = &opts.MaxTokens
		} else {
			req.MaxCompletionTokens = &opts.MaxTokens
		}
	}
	if opts.Seed != 0 {
		if o.provider == ProviderMistral {
			req.RandomSeed = &opts.Seed
		} else {
			req.Seed = &opts.Seed
		}
	}
	if opts.JSONMode {
		req.ResponseFormat = ResponseFormatJSON
	}

	for _, tool := range opts.Tools {
		t, err := toolFromTool(tool)
		if err != nil {
			return nil, fmt.Errorf("failed to convert llms tool to openai tool: %w", err)
		}
		req.Tools = append(req.Tools, t)
	}

	// if o.client.ResponseFormat is set, use it for the request
	if o.client.ResponseFormat != nil {
		req.ResponseFormat = o.client.ResponseFormat
	}

	result, err := o.client.CreateChat(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(result.Choices) == 0 {
		return nil, ErrEmptyResponse
	}

	choices := make([]*llms.ContentChoice, len(result.Choices))
	for i, c := range result.Choices {
		choices[i] = &llms.ContentChoice{
			Content:          c.Message.Content,
			ReasoningContent: c.Message.ReasoningContent,
			StopReason:       fmt.Sprint(c.FinishReason),
			GenerationInfo: map[string]any{
				"CompletionTokens":  result.Usage.CompletionTokens,
				"PromptTokens":      result.Usage.PromptTokens,
				"TotalTokens":       result.Usage.TotalTokens,
				"ReasoningTokens":   result.Usage.CompletionTokensDetails.ReasoningTokens,
				"PromptAudioTokens": result.Usage.PromptTokensDetails.AudioTokens,
				// Provider-neutral usage fields.
				"ThinkingContent":                    c.Message.ReasoningContent,                           // Standardized field
				"ThinkingTokens":                     result.Usage.CompletionTokensDetails.ReasoningTokens, // Standardized field
				"PromptCachedTokens":                 result.Usage.PromptTokensDetails.CachedTokens,
				"CompletionAudioTokens":              result.Usage.CompletionTokensDetails.AudioTokens,
				"CompletionReasoningTokens":          result.Usage.CompletionTokensDetails.ReasoningTokens,
				"CompletionAcceptedPredictionTokens": result.Usage.CompletionTokensDetails.AcceptedPredictionTokens,
				"CompletionRejectedPredictionTokens": result.Usage.CompletionTokensDetails.RejectedPredictionTokens,
			},
		}

		for _, tool := range c.Message.ToolCalls {
			choices[i].ToolCalls = append(choices[i].ToolCalls, llms.ToolCall{
				ID:   tool.ID,
				Type: string(tool.Type),
				FunctionCall: &llms.FunctionCall{
					Name:      tool.Function.Name,
					Arguments: tool.Function.Arguments,
				},
			})
		}
	}
	response := &llms.ContentResponse{Choices: choices}
	if o.CallbacksHandler != nil {
		o.CallbacksHandler.HandleLLMGenerateContentEnd(ctx, response)
	}
	return response, nil
}

func validateCallOptions(opts llms.CallOptions) error {
	if len(opts.Functions) > 0 {
		return fmt.Errorf("legacy functions are unsupported; use tools")
	}
	if opts.FunctionCallBehavior != "" {
		return fmt.Errorf("legacy function_call is unsupported; use tool_choice")
	}
	return nil
}

func (o *LLM) generateResponse(
	ctx context.Context,
	messages []llms.MessageContent,
	opts llms.CallOptions,
) (*llms.ContentResponse, error) {
	if opts.N > 1 {
		return nil, fmt.Errorf("Responses API does not support multiple choices")
	}
	if len(opts.StopWords) > 0 {
		return nil, fmt.Errorf("Responses API does not support stop sequences")
	}
	if opts.Seed != 0 {
		return nil, fmt.Errorf("Responses API does not support seed")
	}
	if opts.FrequencyPenalty != 0 || opts.PresencePenalty != 0 {
		return nil, fmt.Errorf("Responses API does not support frequency or presence penalties")
	}

	effectiveModel := opts.Model
	if effectiveModel == "" {
		effectiveModel = o.model
	}
	requestOptions := requestOptionsFrom(&opts)
	inputs, err := responseInputsFromMessages(
		messages,
		requestOptions.DeveloperRole || usesDeveloperRole(o.provider, effectiveModel),
	)
	if err != nil {
		return nil, err
	}

	apiMetadata := make(map[string]any)
	for key, value := range opts.Metadata {
		if key == "thinking_config" || key == requestOptionsMetadataKey {
			continue
		}
		apiMetadata[key] = value
	}
	if len(apiMetadata) == 0 {
		apiMetadata = nil
	}

	configuredReasoningEffort := reasoningEffort(opts)
	reasoningEffort, providerExtraBody := providerReasoningOptions(o.provider, configuredReasoningEffort)
	request := &openaiclient.ResponseRequest{
		Model:             effectiveModel,
		Input:             inputs,
		ToolChoice:        responseToolChoice(opts.ToolChoice),
		ParallelToolCalls: requestOptions.ParallelToolCalls,
		ServiceTier:       strings.TrimSpace(requestOptions.ServiceTier),
		Store:             requestOptions.Store,
		Metadata:          apiMetadata,
		PromptCacheKey:    strings.TrimSpace(requestOptions.PromptCacheKey),
		SafetyIdentifier:  strings.TrimSpace(requestOptions.SafetyIdentifier),
		ExtraBody:         mergeExtraBody(providerExtraBody, o.client.ExtraBody, requestOptions.ExtraBody),
	}
	if reasoningEffort != "" {
		request.Reasoning = &openaiclient.ResponseReasoning{Effort: reasoningEffort}
	}
	if !omitSamplingParameters(o.provider, effectiveModel, configuredReasoningEffort) {
		if requestOptions.Temperature != nil {
			request.Temperature = requestOptions.Temperature
		} else {
			request.Temperature = optionalFloat64(opts.Temperature)
		}
		request.TopP = optionalFloat64(opts.TopP)
	}
	if opts.MaxTokens > 0 {
		request.MaxOutputTokens = &opts.MaxTokens
	}

	for _, tool := range opts.Tools {
		responseTool, err := responseToolFromTool(tool)
		if err != nil {
			return nil, err
		}
		request.Tools = append(request.Tools, responseTool)
	}
	textConfig := responseTextConfig(o.client.ResponseFormat, opts.JSONMode, requestOptions.Verbosity)
	if textConfig != nil {
		request.Text = textConfig
	}

	result, err := o.client.CreateResponse(ctx, request)
	if err != nil {
		return nil, err
	}
	choice, err := contentChoiceFromResponse(result)
	if err != nil {
		return nil, err
	}
	response := &llms.ContentResponse{Choices: []*llms.ContentChoice{choice}}
	if opts.StreamingFunc != nil && choice.Content != "" {
		if err := opts.StreamingFunc(ctx, []byte(choice.Content)); err != nil {
			return nil, fmt.Errorf("streaming func returned an error: %w", err)
		}
	}
	if opts.StreamingReasoningFunc != nil && (choice.ReasoningContent != "" || choice.Content != "") {
		if err := opts.StreamingReasoningFunc(ctx, []byte(choice.ReasoningContent), []byte(choice.Content)); err != nil {
			return nil, fmt.Errorf("streaming reasoning func returned an error: %w", err)
		}
	}
	if o.CallbacksHandler != nil {
		o.CallbacksHandler.HandleLLMGenerateContentEnd(ctx, response)
	}
	return response, nil
}

func responseInputsFromMessages(messages []llms.MessageContent, useDeveloperRole bool) ([]openaiclient.ResponseInputItem, error) {
	inputs := make([]openaiclient.ResponseInputItem, 0, len(messages))
	for _, message := range messages {
		if message.Role == llms.ChatMessageTypeTool {
			if len(message.Parts) != 1 {
				return nil, fmt.Errorf("expected exactly one part for role %v, got %v", message.Role, len(message.Parts))
			}
			toolResponse, ok := message.Parts[0].(llms.ToolCallResponse)
			if !ok {
				return nil, fmt.Errorf("expected part of type ToolCallResponse for role %v, got %T", message.Role, message.Parts[0])
			}
			output := toolResponse.Content
			inputs = append(inputs, openaiclient.ResponseInputItem{
				Type:   "function_call_output",
				CallID: toolResponse.ToolCallID,
				Output: &output,
			})
			continue
		}

		role, err := responseRole(message.Role, useDeveloperRole)
		if err != nil {
			return nil, err
		}
		parts, _ := extractReasoningParts(message.Parts)
		content := make([]openaiclient.ResponseContent, 0, len(parts))
		toolCalls := make([]llms.ToolCall, 0)
		for _, part := range parts {
			switch value := part.(type) {
			case llms.TextContent:
				contentType := "input_text"
				if role == RoleAssistant {
					contentType = "output_text"
				}
				content = append(content, openaiclient.ResponseContent{Type: contentType, Text: value.Text})
			case llms.ImageURLContent:
				content = append(content, openaiclient.ResponseContent{
					Type:     "input_image",
					ImageURL: value.URL,
					Detail:   value.Detail,
				})
			case llms.BinaryContent:
				if !strings.HasPrefix(strings.ToLower(value.MIMEType), "image/") {
					return nil, fmt.Errorf("Responses API binary content type %q is unsupported", value.MIMEType)
				}
				content = append(content, openaiclient.ResponseContent{
					Type:     "input_image",
					ImageURL: "data:" + value.MIMEType + ";base64," + base64.StdEncoding.EncodeToString(value.Data),
				})
			case llms.ToolCall:
				toolCalls = append(toolCalls, value)
			default:
				return nil, fmt.Errorf("Responses API content part %T is unsupported", part)
			}
		}
		if len(content) > 0 || len(toolCalls) == 0 {
			input := openaiclient.ResponseInputItem{Role: role, Content: content}
			if role == RoleAssistant {
				input.Type = "message"
				input.Status = "completed"
			}
			inputs = append(inputs, input)
		}
		for _, toolCall := range toolCalls {
			if toolCall.FunctionCall == nil {
				return nil, fmt.Errorf("tool call %q has no function call", toolCall.ID)
			}
			inputs = append(inputs, openaiclient.ResponseInputItem{
				Type:      "function_call",
				Status:    "completed",
				CallID:    toolCall.ID,
				Name:      toolCall.FunctionCall.Name,
				Arguments: toolCall.FunctionCall.Arguments,
			})
		}
	}
	return inputs, nil
}

func responseRole(role llms.ChatMessageType, useDeveloperRole bool) (string, error) {
	switch role {
	case llms.ChatMessageTypeSystem:
		if useDeveloperRole {
			return RoleDeveloper, nil
		}
		return RoleSystem, nil
	case llms.ChatMessageTypeAI:
		return RoleAssistant, nil
	case llms.ChatMessageTypeHuman, llms.ChatMessageTypeGeneric:
		return RoleUser, nil
	default:
		return "", fmt.Errorf("role %v not supported", role)
	}
}

func responseToolFromTool(tool llms.Tool) (openaiclient.ResponseTool, error) {
	if tool.Type != string(openaiclient.ToolTypeFunction) || tool.Function == nil {
		return openaiclient.ResponseTool{}, fmt.Errorf("Responses API tool type %q is unsupported", tool.Type)
	}
	return openaiclient.ResponseTool{
		Type:        openaiclient.ToolTypeFunction,
		Name:        tool.Function.Name,
		Description: tool.Function.Description,
		Parameters:  tool.Function.Parameters,
		Strict:      tool.Function.Strict,
	}, nil
}

func responseToolChoice(toolChoice any) any {
	if toolChoice == nil {
		return nil
	}
	payload, err := json.Marshal(toolChoice)
	if err != nil {
		return toolChoice
	}
	var choice map[string]any
	if err := json.Unmarshal(payload, &choice); err != nil || choice["type"] != "function" {
		return toolChoice
	}
	function, ok := choice["function"].(map[string]any)
	if !ok {
		return toolChoice
	}
	name, _ := function["name"].(string)
	if strings.TrimSpace(name) == "" {
		return toolChoice
	}
	return map[string]any{"type": "function", "name": name}
}

func responseTextConfig(
	responseFormat *openaiclient.ResponseFormat,
	jsonMode bool,
	verbosity string,
) *openaiclient.ResponseTextConfig {
	config := &openaiclient.ResponseTextConfig{Verbosity: strings.TrimSpace(verbosity)}
	if responseFormat != nil {
		format := &openaiclient.ResponseTextFormat{Type: responseFormat.Type}
		if responseFormat.JSONSchema != nil {
			format.Name = responseFormat.JSONSchema.Name
			format.Strict = responseFormat.JSONSchema.Strict
			format.Schema = responseFormat.JSONSchema.Schema
		}
		config.Format = format
	} else if jsonMode {
		config.Format = &openaiclient.ResponseTextFormat{Type: "json_object"}
	}
	if config.Format == nil && config.Verbosity == "" {
		return nil
	}
	return config
}

func contentChoiceFromResponse(response *openaiclient.Response) (*llms.ContentChoice, error) {
	if response == nil {
		return nil, ErrEmptyResponse
	}
	var content strings.Builder
	var reasoning strings.Builder
	toolCalls := make([]llms.ToolCall, 0)
	for _, output := range response.Output {
		switch output.Type {
		case "message":
			for _, part := range output.Content {
				switch part.Type {
				case "output_text":
					content.WriteString(part.Text)
				case "refusal":
					content.WriteString(part.Refusal)
				}
			}
		case "function_call":
			toolCalls = append(toolCalls, llms.ToolCall{
				ID:   output.CallID,
				Type: string(openaiclient.ToolTypeFunction),
				FunctionCall: &llms.FunctionCall{
					Name:      output.Name,
					Arguments: output.Arguments,
				},
			})
		case "reasoning":
			for _, summary := range output.Summary {
				if summary.Text == "" {
					continue
				}
				if reasoning.Len() > 0 {
					reasoning.WriteString("\n")
				}
				reasoning.WriteString(summary.Text)
			}
		}
	}
	if content.Len() == 0 && len(toolCalls) == 0 {
		if response.Status == "failed" && response.Error != nil {
			return nil, fmt.Errorf("Responses API returned %v: %s", response.Error.Code, response.Error.Message)
		}
		return nil, ErrEmptyResponse
	}
	stopReason := "stop"
	if len(toolCalls) > 0 {
		stopReason = "tool_calls"
	} else if response.IncompleteDetails != nil && response.IncompleteDetails.Reason != "" {
		stopReason = response.IncompleteDetails.Reason
	} else if response.Status != "" && response.Status != "completed" {
		stopReason = response.Status
	}
	return &llms.ContentChoice{
		Content:          content.String(),
		ReasoningContent: reasoning.String(),
		StopReason:       stopReason,
		ToolCalls:        toolCalls,
		GenerationInfo: map[string]any{
			"CompletionTokens":          response.Usage.OutputTokens,
			"PromptTokens":              response.Usage.InputTokens,
			"TotalTokens":               response.Usage.TotalTokens,
			"ReasoningTokens":           response.Usage.OutputTokensDetails.ReasoningTokens,
			"ThinkingContent":           reasoning.String(),
			"ThinkingTokens":            response.Usage.OutputTokensDetails.ReasoningTokens,
			"PromptCachedTokens":        response.Usage.InputTokensDetails.CachedTokens,
			"CompletionReasoningTokens": response.Usage.OutputTokensDetails.ReasoningTokens,
		},
	}, nil
}

func validateCompletionCallOptions(opts llms.CallOptions) error {
	if err := validateCallOptions(opts); err != nil {
		return err
	}
	if len(opts.Tools) > 0 || opts.ToolChoice != nil {
		return fmt.Errorf("tools are unsupported for text completions")
	}
	if opts.JSONMode {
		return fmt.Errorf("JSON mode is unsupported for text completions")
	}
	return nil
}

func reasoningEffort(opts llms.CallOptions) string {
	config := llms.GetThinkingConfig(&opts)
	if config == nil || config.Mode == "" || config.Mode == llms.ThinkingModeAuto {
		return ""
	}
	return string(config.Mode)
}

func providerReasoningOptions(provider Provider, effort string) (string, map[string]any) {
	effort = strings.TrimSpace(effort)
	if effort == "" {
		return effort, nil
	}
	switch provider {
	case ProviderDeepSeek:
		thinkingType := "enabled"
		if effort == "none" {
			thinkingType = "disabled"
		}
		return "", map[string]any{"thinking": map[string]any{"type": thinkingType}}
	case ProviderOpenRouter:
		return "", map[string]any{"reasoning": map[string]any{"effort": effort}}
	default:
		return effort, nil
	}
}

func usesMaxTokens(provider Provider) bool {
	switch provider {
	case ProviderDeepSeek, ProviderMistral, ProviderOpenRouter:
		return true
	default:
		return false
	}
}

func usesDeveloperRole(provider Provider, model string) bool {
	return (provider == ProviderOpenAI || provider == ProviderAzure) && isOpenAIReasoningModel(model)
}

func omitSamplingParameters(provider Provider, model, effort string) bool {
	if strings.TrimSpace(effort) != "" {
		return true
	}
	return (provider == ProviderOpenAI || provider == ProviderAzure) && isOpenAIReasoningModel(model)
}

func isOpenAIReasoningModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	for _, prefix := range []string{"o1", "o3", "o4", "o5", "gpt-5"} {
		if model == prefix || strings.HasPrefix(model, prefix+"-") || strings.HasPrefix(model, prefix+".") {
			return true
		}
	}
	return false
}

func isAzureV1URL(baseURL string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimRight(strings.TrimSpace(baseURL), "/")), "/openai/v1")
}

func optionalFloat64(value float64) *float64 {
	if value == 0 {
		return nil
	}
	return &value
}

func mergeExtraBody(inputs ...map[string]any) map[string]any {
	var merged map[string]any
	for _, input := range inputs {
		if len(input) == 0 {
			continue
		}
		if merged == nil {
			merged = make(map[string]any)
		}
		for key, value := range input {
			merged[key] = value
		}
	}
	return merged
}

// SupportsReasoning implements the ReasoningModel interface.
// Returns true if the current model supports reasoning/thinking tokens.
func (o *LLM) SupportsReasoning() bool {
	if o.provider == ProviderDeepSeek || o.provider == ProviderGemini || o.provider == ProviderXAI {
		return true
	}
	return isOpenAIReasoningModel(o.model)
}

// CreateEmbedding creates embeddings for the given input texts.
func (o *LLM) CreateEmbedding(ctx context.Context, inputTexts []string) ([][]float32, error) {
	embeddings, err := o.client.CreateEmbedding(ctx, &openaiclient.EmbeddingRequest{
		Input: inputTexts,
		Model: o.client.EmbeddingModel,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create openai embeddings: %w", err)
	}
	if len(embeddings) == 0 {
		return nil, ErrEmptyResponse
	}
	if len(inputTexts) != len(embeddings) {
		return embeddings, ErrUnexpectedResponseLength
	}
	return embeddings, nil
}

// extractReasoningParts splits ReasoningPart entries out of the part list and
// concatenates their text. The returned slice preserves the original order of
// the remaining parts. If no reasoning parts are present, the original slice
// is returned unchanged and reasoning is "".
func extractReasoningParts(in []llms.ContentPart) ([]llms.ContentPart, string) {
	var reasoning strings.Builder
	kept := in
	hasReasoning := false
	for _, p := range in {
		if _, ok := p.(parts.ReasoningPart); ok {
			hasReasoning = true
			break
		}
	}
	if !hasReasoning {
		return in, ""
	}
	kept = make([]llms.ContentPart, 0, len(in))
	for _, p := range in {
		if rp, ok := p.(parts.ReasoningPart); ok {
			if reasoning.Len() > 0 {
				reasoning.WriteString("\n")
			}
			reasoning.WriteString(rp.Text)
			continue
		}
		kept = append(kept, p)
	}
	return kept, reasoning.String()
}

// ExtractToolParts extracts the tool parts from a message.
func ExtractToolParts(msg *ChatMessage) ([]llms.ContentPart, []llms.ToolCall) {
	var content []llms.ContentPart
	var toolCalls []llms.ToolCall
	for _, part := range msg.MultiContent {
		switch p := part.(type) {
		case llms.TextContent:
			content = append(content, p)
		case llms.ImageURLContent:
			content = append(content, p)
		case llms.BinaryContent:
			content = append(content, p)
		case llms.ToolCall:
			toolCalls = append(toolCalls, p)
		}
	}
	return content, toolCalls
}

// toolFromTool converts an llms.Tool to a Tool.
func toolFromTool(t llms.Tool) (openaiclient.Tool, error) {
	tool := openaiclient.Tool{
		Type: openaiclient.ToolType(t.Type),
	}
	switch t.Type {
	case string(openaiclient.ToolTypeFunction):
		tool.Function = openaiclient.FunctionDefinition{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
			Strict:      t.Function.Strict,
		}
	default:
		return openaiclient.Tool{}, fmt.Errorf("tool type %v not supported", t.Type)
	}
	return tool, nil
}

// toolCallsFromToolCalls converts a slice of llms.ToolCall to a slice of ToolCall.
func toolCallsFromToolCalls(tcs []llms.ToolCall) []openaiclient.ToolCall {
	toolCalls := make([]openaiclient.ToolCall, len(tcs))
	for i, tc := range tcs {
		toolCalls[i] = toolCallFromToolCall(tc)
	}
	return toolCalls
}

// toolCallFromToolCall converts an llms.ToolCall to a ToolCall.
func toolCallFromToolCall(tc llms.ToolCall) openaiclient.ToolCall {
	return openaiclient.ToolCall{
		ID:   tc.ID,
		Type: openaiclient.ToolType(tc.Type),
		Function: openaiclient.ToolFunction{
			Name:      tc.FunctionCall.Name,
			Arguments: tc.FunctionCall.Arguments,
		},
	}
}
