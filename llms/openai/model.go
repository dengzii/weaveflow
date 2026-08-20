package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/llms"
	"github.com/dengzii/weaveflow/llms/openai/internal/openaiclient"
)

type ChatMessage = openaiclient.ChatMessage

type LLM struct {
	client    *openaiclient.Client
	model     string
	provider  Provider
	apiFormat APIFormat
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
		return nil, fmt.Errorf("azure Responses API requires a base URL ending in /openai/v1")
	}
	return &LLM{
		client:    c,
		model:     c.Model,
		provider:  opt.provider,
		apiFormat: opt.apiFormat,
	}, err
}

func (o *LLM) Name() string {
	return o.client.Model
}

func (o *LLM) Generate(ctx context.Context, request llms.ModelRequest) (*llms.ModelResponse, error) {
	switch request.Mode {
	case "", llms.ModelModeChat:
		if o.apiFormat == APIFormatResponses {
			return o.generateResponse(ctx, request)
		}
		return o.generateChat(ctx, request)
	case llms.ModelModeCompletion:
		return o.generateCompletion(ctx, request)
	default:
		return nil, fmt.Errorf("openai model request mode %q is unsupported", request.Mode)
	}
}

func (o *LLM) generateCompletion(ctx context.Context, request llms.ModelRequest) (*llms.ModelResponse, error) {
	if len(request.Tools) > 0 || request.ToolChoice != nil {
		return nil, fmt.Errorf("tools are unsupported for text completions")
	}
	if len(request.ResponseSchema) > 0 {
		return nil, fmt.Errorf("structured output is unsupported for text completions")
	}
	if o.apiFormat == APIFormatResponses || request.Thinking != "" && request.Thinking != llms.ThinkingModeAuto {
		request.Mode = llms.ModelModeChat
		request.Messages = []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, request.Prompt)}
		request.Prompt = ""
		if o.apiFormat == APIFormatResponses {
			return o.generateResponse(ctx, request)
		}
		return o.generateChat(ctx, request)
	}
	requestOptions := requestOptionsFrom(request)
	result, err := o.client.CreateCompletion(ctx, &openaiclient.CompletionRequest{
		Model:            request.Model,
		Prompt:           request.Prompt,
		MaxTokens:        request.MaxTokens,
		Temperature:      float64Value(request.Temperature),
		TopP:             float64Value(request.TopP),
		N:                request.CandidateCount,
		StopWords:        request.StopWords,
		FrequencyPenalty: float64Value(request.FrequencyPenalty),
		PresencePenalty:  float64Value(request.PresencePenalty),
		Seed:             intValue(request.Seed),
		ExtraBody:        mergeExtraBody(o.client.ExtraBody, requestOptions.ExtraBody),
	})
	if err != nil {
		return nil, MapError(err)
	}
	choices := make([]*llms.ModelChoice, len(result.Choices))
	for index, choice := range result.Choices {
		choices[index] = &llms.ModelChoice{Content: choice.Text, StopReason: choice.FinishReason}
	}
	effectiveModel := request.Model
	if effectiveModel == "" {
		effectiveModel = o.model
	}
	response := &llms.ModelResponse{
		Model:   effectiveModel,
		Choices: choices,
		Usage: llms.ModelUsage{
			InputTokens:  result.Usage.PromptTokens,
			OutputTokens: result.Usage.CompletionTokens,
			TotalTokens:  result.Usage.TotalTokens,
		},
	}
	if request.Stream != nil && len(choices) > 0 && choices[0] != nil && choices[0].Content != "" {
		if err := request.Stream(ctx, llms.ModelStreamEvent{CallID: request.CallID, Model: response.Model, Type: llms.ModelStreamContent, Text: choices[0].Content}); err != nil {
			return nil, fmt.Errorf("model stream handler: %w", err)
		}
	}
	return response, nil
}

func (o *LLM) generateChat(ctx context.Context, request llms.ModelRequest) (*llms.ModelResponse, error) { //nolint:cyclop,funlen
	effectiveModel := request.Model
	if effectiveModel == "" {
		effectiveModel = o.model
	}

	requestOptions := requestOptionsFrom(request)
	useDeveloperRole := requestOptions.DeveloperRole || usesDeveloperRole(o.provider, effectiveModel)

	chatMsgs := make([]*ChatMessage, 0, len(request.Messages))
	for _, mc := range request.Messages {
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

			// Tool messages carry exactly one typed result linked to the originating call.
			if len(mc.Parts) != 1 {
				return nil, fmt.Errorf("expected exactly one part for role %v, got %v", mc.Role, len(mc.Parts))
			}
			switch p := mc.Parts[0].(type) {
			case llms.ToolResult:
				msg.ToolCallID = p.ToolCallID
				msg.Content = llms.ToolResultText(p)
			default:
				return nil, fmt.Errorf("expected part of type ToolResult for role %v, got %T", mc.Role, mc.Parts[0])
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
	apiMetadata := make(map[string]any, len(request.Metadata))
	for key, value := range request.Metadata {
		apiMetadata[key] = value
	}
	// Only include metadata if there are actual values to send
	if len(apiMetadata) == 0 {
		apiMetadata = nil
	}

	configuredReasoningEffort := reasoningEffort(request)
	reasoningEffort, providerExtraBody := providerReasoningOptions(o.provider, configuredReasoningEffort)
	extraBody := mergeExtraBody(providerExtraBody, o.client.ExtraBody, requestOptions.ExtraBody)
	req := &openaiclient.ChatRequest{
		Model:                  request.Model,
		StopWords:              request.StopWords,
		Messages:               chatMsgs,
		StreamingReasoningFunc: modelStreamHandler(request),
		N:                      request.CandidateCount,
		FrequencyPenalty:       request.FrequencyPenalty,
		PresencePenalty:        request.PresencePenalty,
		ReasoningEffort:        reasoningEffort,
		ToolChoice:             request.ToolChoice,
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
		req.Temperature = request.Temperature
		req.TopP = request.TopP
	}
	if request.MaxTokens > 0 {
		if usesMaxTokens(o.provider) {
			req.MaxTokens = &request.MaxTokens
		} else {
			req.MaxCompletionTokens = &request.MaxTokens
		}
	}
	if request.Seed != nil {
		if o.provider == ProviderMistral {
			req.RandomSeed = request.Seed
		} else {
			req.Seed = request.Seed
		}
	}

	for _, tool := range request.Tools {
		t, err := toolFromTool(tool)
		if err != nil {
			return nil, fmt.Errorf("failed to convert LLM tool to OpenAI tool: %w", err)
		}
		req.Tools = append(req.Tools, t)
	}

	// if o.client.ResponseFormat is set, use it for the request
	if len(request.ResponseSchema) > 0 {
		name := strings.TrimSpace(request.ResponseName)
		if name == "" {
			name = "response"
		}
		req.ResponseFormat = &ResponseFormat{
			Type: "json_schema",
			JSONSchema: &ResponseFormatJSONSchema{
				Name:   name,
				Strict: request.StrictResponse,
				Schema: request.ResponseSchema,
			},
		}
	} else if o.client.ResponseFormat != nil {
		req.ResponseFormat = o.client.ResponseFormat
	}

	result, err := o.client.CreateChat(ctx, req)
	if err != nil {
		return nil, MapError(err)
	}
	if len(result.Choices) == 0 {
		return nil, ErrEmptyResponse
	}

	choices := make([]*llms.ModelChoice, len(result.Choices))
	for i, c := range result.Choices {
		choices[i] = &llms.ModelChoice{
			Content:          c.Message.Content,
			ReasoningContent: c.Message.ReasoningContent,
			StopReason:       fmt.Sprint(c.FinishReason),
		}

		for _, tool := range c.Message.ToolCalls {
			choices[i].ToolCalls = append(choices[i].ToolCalls, llms.ToolCall{
				ID:   tool.ID,
				Type: string(tool.Type),
				FunctionCall: &llms.FunctionCall{
					Name:      tool.Function.Name,
					Arguments: rawArguments(tool.Function.Arguments),
				},
			})
		}
	}
	return &llms.ModelResponse{
		ID:      result.ID,
		Model:   firstNonEmpty(result.Model, effectiveModel),
		Choices: choices,
		Usage: llms.ModelUsage{
			InputTokens:       result.Usage.PromptTokens,
			OutputTokens:      result.Usage.CompletionTokens,
			TotalTokens:       result.Usage.TotalTokens,
			CachedInputTokens: result.Usage.PromptTokensDetails.CachedTokens,
			ReasoningTokens:   result.Usage.CompletionTokensDetails.ReasoningTokens,
		},
	}, nil
}

func modelStreamHandler(request llms.ModelRequest) func(context.Context, []byte, []byte) error {
	if request.Stream == nil {
		return nil
	}
	return func(ctx context.Context, reasoningChunk, contentChunk []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if text := string(reasoningChunk); text != "" {
			if err := request.Stream(ctx, llms.ModelStreamEvent{CallID: request.CallID, Model: request.Model, Type: llms.ModelStreamReasoning, Text: text}); err != nil {
				return err
			}
		}
		if text := string(contentChunk); text != "" && !strings.HasPrefix(strings.TrimSpace(text), "[{") {
			if err := request.Stream(ctx, llms.ModelStreamEvent{CallID: request.CallID, Model: request.Model, Type: llms.ModelStreamContent, Text: text}); err != nil {
				return err
			}
		}
		return nil
	}
}

func (o *LLM) generateResponse(
	ctx context.Context,
	modelRequest llms.ModelRequest,
) (*llms.ModelResponse, error) {
	if modelRequest.CandidateCount > 1 {
		return nil, fmt.Errorf("responses API does not support multiple choices")
	}
	if len(modelRequest.StopWords) > 0 {
		return nil, fmt.Errorf("responses API does not support stop sequences")
	}
	if modelRequest.Seed != nil {
		return nil, fmt.Errorf("responses API does not support seed")
	}
	if modelRequest.FrequencyPenalty != nil || modelRequest.PresencePenalty != nil {
		return nil, fmt.Errorf("responses API does not support frequency or presence penalties")
	}

	effectiveModel := modelRequest.Model
	if effectiveModel == "" {
		effectiveModel = o.model
	}
	requestOptions := requestOptionsFrom(modelRequest)
	inputs, err := responseInputsFromMessages(
		modelRequest.Messages,
		requestOptions.DeveloperRole || usesDeveloperRole(o.provider, effectiveModel),
	)
	if err != nil {
		return nil, err
	}

	apiMetadata := make(map[string]any, len(modelRequest.Metadata))
	for key, value := range modelRequest.Metadata {
		apiMetadata[key] = value
	}
	if len(apiMetadata) == 0 {
		apiMetadata = nil
	}

	configuredReasoningEffort := reasoningEffort(modelRequest)
	reasoningEffort, providerExtraBody := providerReasoningOptions(o.provider, configuredReasoningEffort)
	providerRequest := &openaiclient.ResponseRequest{
		Model:             effectiveModel,
		Input:             inputs,
		ToolChoice:        responseToolChoice(modelRequest.ToolChoice),
		ParallelToolCalls: requestOptions.ParallelToolCalls,
		ServiceTier:       strings.TrimSpace(requestOptions.ServiceTier),
		Store:             requestOptions.Store,
		Metadata:          apiMetadata,
		PromptCacheKey:    strings.TrimSpace(requestOptions.PromptCacheKey),
		SafetyIdentifier:  strings.TrimSpace(requestOptions.SafetyIdentifier),
		ExtraBody:         mergeExtraBody(providerExtraBody, o.client.ExtraBody, requestOptions.ExtraBody),
	}
	if reasoningEffort != "" {
		providerRequest.Reasoning = &openaiclient.ResponseReasoning{Effort: reasoningEffort}
	}
	if !omitSamplingParameters(o.provider, effectiveModel, configuredReasoningEffort) {
		providerRequest.Temperature = modelRequest.Temperature
		providerRequest.TopP = modelRequest.TopP
	}
	if modelRequest.MaxTokens > 0 {
		providerRequest.MaxOutputTokens = &modelRequest.MaxTokens
	}

	for _, tool := range modelRequest.Tools {
		responseTool, err := responseToolFromTool(tool)
		if err != nil {
			return nil, err
		}
		providerRequest.Tools = append(providerRequest.Tools, responseTool)
	}
	responseFormat := o.client.ResponseFormat
	if len(modelRequest.ResponseSchema) > 0 {
		name := strings.TrimSpace(modelRequest.ResponseName)
		if name == "" {
			name = "response"
		}
		responseFormat = &ResponseFormat{
			Type: "json_schema",
			JSONSchema: &ResponseFormatJSONSchema{
				Name:   name,
				Strict: modelRequest.StrictResponse,
				Schema: modelRequest.ResponseSchema,
			},
		}
	}
	textConfig := responseTextConfig(responseFormat, requestOptions.Verbosity)
	if textConfig != nil {
		providerRequest.Text = textConfig
	}

	result, err := o.client.CreateResponse(ctx, providerRequest)
	if err != nil {
		return nil, MapError(err)
	}
	choice, err := contentChoiceFromResponse(result)
	if err != nil {
		return nil, err
	}
	response := &llms.ModelResponse{
		ID:      result.ID,
		Model:   firstNonEmpty(result.Model, effectiveModel),
		Choices: []*llms.ModelChoice{choice},
		Usage: llms.ModelUsage{
			InputTokens:       result.Usage.InputTokens,
			OutputTokens:      result.Usage.OutputTokens,
			TotalTokens:       result.Usage.TotalTokens,
			CachedInputTokens: result.Usage.InputTokensDetails.CachedTokens,
			ReasoningTokens:   result.Usage.OutputTokensDetails.ReasoningTokens,
		},
	}
	if modelRequest.Stream != nil {
		if choice.ReasoningContent != "" {
			if err := modelRequest.Stream(ctx, llms.ModelStreamEvent{CallID: modelRequest.CallID, Model: response.Model, Type: llms.ModelStreamReasoning, Text: choice.ReasoningContent}); err != nil {
				return nil, fmt.Errorf("model stream handler: %w", err)
			}
		}
		if choice.Content != "" {
			if err := modelRequest.Stream(ctx, llms.ModelStreamEvent{CallID: modelRequest.CallID, Model: response.Model, Type: llms.ModelStreamContent, Text: choice.Content}); err != nil {
				return nil, fmt.Errorf("model stream handler: %w", err)
			}
		}
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
			toolResponse, ok := message.Parts[0].(llms.ToolResult)
			if !ok {
				return nil, fmt.Errorf("expected part of type ToolResult for role %v, got %T", message.Role, message.Parts[0])
			}
			output := llms.ToolResultText(toolResponse)
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
					return nil, fmt.Errorf("responses API binary content type %q is unsupported", value.MIMEType)
				}
				content = append(content, openaiclient.ResponseContent{
					Type:     "input_image",
					ImageURL: "data:" + value.MIMEType + ";base64," + base64.StdEncoding.EncodeToString(value.Data),
				})
			case llms.ToolCall:
				toolCalls = append(toolCalls, value)
			default:
				return nil, fmt.Errorf("responses API content part %T is unsupported", part)
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
				Arguments: string(toolCall.FunctionCall.Arguments),
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

func responseToolFromTool(tool llms.ToolDefinition) (openaiclient.ResponseTool, error) {
	if tool.Type != string(openaiclient.ToolTypeFunction) || tool.Function == nil {
		return openaiclient.ResponseTool{}, fmt.Errorf("responses API tool type %q is unsupported", tool.Type)
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
	}
	if config.Format == nil && config.Verbosity == "" {
		return nil
	}
	return config
}

func contentChoiceFromResponse(response *openaiclient.Response) (*llms.ModelChoice, error) {
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
					Arguments: rawArguments(output.Arguments),
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
			return nil, fmt.Errorf("responses API returned %v: %s", response.Error.Code, response.Error.Message)
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
	return &llms.ModelChoice{
		Content:          content.String(),
		ReasoningContent: reasoning.String(),
		StopReason:       stopReason,
		ToolCalls:        toolCalls,
	}, nil
}

func reasoningEffort(request llms.ModelRequest) string {
	if request.Thinking == "" || request.Thinking == llms.ThinkingModeAuto {
		return ""
	}
	return string(request.Thinking)
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
		return nil, fmt.Errorf("failed to create OpenAI embeddings: %w", err)
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
	hasReasoning := false
	for _, p := range in {
		if _, ok := p.(llms.ReasoningContent); ok {
			hasReasoning = true
			break
		}
	}
	if !hasReasoning {
		return in, ""
	}
	kept := make([]llms.ContentPart, 0, len(in))
	for _, p := range in {
		if rp, ok := p.(llms.ReasoningContent); ok {
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

// toolFromTool converts a model tool definition to an OpenAI tool.
func toolFromTool(t llms.ToolDefinition) (openaiclient.Tool, error) {
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
			Arguments: string(tc.FunctionCall.Arguments),
		},
	}
}

func rawArguments(arguments string) json.RawMessage {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" || !json.Valid([]byte(arguments)) {
		return json.RawMessage("{}")
	}
	return json.RawMessage(arguments)
}

func float64Value(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func intValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
