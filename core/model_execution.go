package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/dengzii/weaveflow/llms"
	"github.com/dengzii/weaveflow/state"
)

type ModelCallStage string

const (
	ModelCallStarted   ModelCallStage = "started"
	ModelCallStream    ModelCallStage = "stream"
	ModelCallCompleted ModelCallStage = "completed"
	ModelCallFailed    ModelCallStage = "failed"
)

type ModelCallEvent struct {
	Stage    ModelCallStage
	Request  llms.ModelRequest
	Stream   llms.ModelStreamEvent
	Response *llms.ModelResponse
	Err      error
	// CloneError reports observer fields omitted because they could not be
	// safely deep-cloned. It never exposes the original mutable value.
	CloneError error
}

type ModelCallObserver func(context.Context, ModelCallEvent) error

type modelCallObserverKey struct{}

func WithModelCallObserver(ctx context.Context, observer ModelCallObserver) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, modelCallObserverKey{}, observer)
}

func GenerateModel(ctx context.Context, model llms.Model, request llms.ModelRequest) (*llms.ModelResponse, error) {
	if model == nil {
		return nil, errors.New("model is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	request, err := cloneModelRequest(request)
	if err != nil {
		return nil, NewExecutionError(ErrorInvalidInput, fmt.Sprintf("model request cannot be safely cloned: %v", err), err, nil)
	}
	if request.CallID == "" {
		request.CallID = newModelCallID()
	}
	if request.Mode == "" {
		request.Mode = llms.ModelModeChat
	}
	if err := validateModelRequest(request); err != nil {
		return nil, err
	}
	validationRequest := request

	observer, _ := ctx.Value(modelCallObserverKey{}).(ModelCallObserver)
	if observer != nil {
		if err := notifyModelCallObserver(ctx, observer, ModelCallEvent{Stage: ModelCallStarted, Request: request}); err != nil {
			return nil, err
		}
	}
	originalStream := request.Stream
	if observer != nil || originalStream != nil {
		request.Stream = func(streamCtx context.Context, event llms.ModelStreamEvent) error {
			if err := streamCtx.Err(); err != nil {
				return err
			}
			if event.CallID == "" {
				event.CallID = request.CallID
			}
			if observer != nil {
				if err := notifyModelCallObserver(streamCtx, observer, ModelCallEvent{Stage: ModelCallStream, Request: request, Stream: event}); err != nil {
					return err
				}
			}
			if originalStream != nil {
				return originalStream(streamCtx, event)
			}
			return nil
		}
	}

	providerRequest, err := cloneModelRequest(request)
	if err != nil {
		return nil, NewExecutionError(ErrorInvalidInput, fmt.Sprintf("model request cannot be safely cloned: %v", err), err, nil)
	}
	response, err := model.Generate(ctx, providerRequest)
	if err != nil {
		if observer != nil {
			_ = notifyModelCallObserver(ctx, observer, ModelCallEvent{Stage: ModelCallFailed, Request: request, Response: response, Err: err})
		}
		return response, err
	}
	if response == nil {
		err = errors.New("model returned a nil response")
		if observer != nil {
			_ = notifyModelCallObserver(ctx, observer, ModelCallEvent{Stage: ModelCallFailed, Request: request, Err: err})
		}
		return nil, err
	}
	response.Usage = response.Usage.Normalized()
	if response.Model == "" {
		if named, ok := model.(llms.NamedModel); ok {
			response.Model = strings.TrimSpace(named.Name())
		}
	}
	if response.Cost == nil {
		if config, ok := ModelConfigByIDFromContext(ctx, request.ModelID); ok {
			response.Cost = llms.CalculateModelCost(response.Usage, config.Pricing)
		}
	}
	if err := validateStructuredModelResponse(validationRequest, response); err != nil {
		if observer != nil {
			_ = notifyModelCallObserver(ctx, observer, ModelCallEvent{Stage: ModelCallFailed, Request: request, Response: response, Err: err})
		}
		return response, err
	}
	if observer != nil {
		_ = notifyModelCallObserver(ctx, observer, ModelCallEvent{Stage: ModelCallCompleted, Request: request, Response: response})
	}
	return response, nil
}

func validateModelRequest(request llms.ModelRequest) error {
	switch request.Mode {
	case llms.ModelModeChat:
		if len(request.Messages) == 0 {
			return NewExecutionError(ErrorInvalidInput, "chat model request requires messages", nil, nil)
		}
	case llms.ModelModeCompletion:
		if strings.TrimSpace(request.Prompt) == "" {
			return NewExecutionError(ErrorInvalidInput, "completion model request requires a prompt", nil, nil)
		}
	default:
		return NewExecutionError(ErrorInvalidInput, fmt.Sprintf("unsupported model request mode %q", request.Mode), nil, nil)
	}
	for _, tool := range request.Tools {
		if tool.Function == nil || strings.TrimSpace(tool.Function.Name) == "" {
			return NewExecutionError(ErrorInvalidInput, "model tool definition requires a function name", nil, nil)
		}
		if err := state.ValidateJSONSchemaDefinition(tool.Function.Parameters); err != nil {
			return NewExecutionError(ErrorInvalidInput, fmt.Sprintf("model tool %q input schema: %v", tool.Function.Name, err), err, nil)
		}
	}
	if err := state.ValidateJSONSchemaDefinition(request.ResponseSchema); err != nil {
		return NewExecutionError(ErrorInvalidInput, fmt.Sprintf("model response schema: %v", err), err, nil)
	}
	return nil
}

func validateStructuredModelResponse(request llms.ModelRequest, response *llms.ModelResponse) error {
	if !request.ResponseJSON && len(request.ResponseSchema) == 0 {
		return nil
	}
	if len(response.Choices) == 0 || response.Choices[0] == nil {
		return NewExecutionError(ErrorInvalidOutput, "structured model response has no choices", nil, nil)
	}
	choice := response.Choices[0]
	if len(choice.ToolCalls) > 0 {
		return nil
	}
	_, _, err := DecodeStructuredOutput(choice.Content, request.ResponseSchema, request.ResponseJSONCompatibility)
	return err
}

// DecodeStructuredOutput normalizes one schema-valid JSON value from model output.
func DecodeStructuredOutput(content string, schema state.JSONSchema, compatibility bool) (string, any, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", nil, NewExecutionError(ErrorInvalidOutput, "structured output is empty", nil, nil)
	}

	value, err := decodeSingleJSONValue(content)
	if err == nil {
		return normalizeStructuredOutput(value, schema)
	}
	if !compatibility {
		return "", nil, NewExecutionError(ErrorInvalidOutput, fmt.Sprintf("structured output is not a single JSON value: %v", err), err, nil)
	}

	var candidateErr error
	for _, candidate := range compatibleJSONCandidates(content) {
		value, decodeErr := decodeSingleJSONValue(candidate)
		if decodeErr != nil {
			continue
		}
		normalized, parsed, normalizeErr := normalizeStructuredOutput(value, schema)
		if normalizeErr == nil {
			return normalized, parsed, nil
		}
		if candidateErr == nil {
			candidateErr = normalizeErr
		}
	}
	if candidateErr != nil {
		return "", nil, candidateErr
	}
	return "", nil, NewExecutionError(ErrorInvalidOutput, "structured output does not contain a JSON value matching the response schema", err, nil)
}

func decodeSingleJSONValue(content string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
		return nil, fmt.Errorf("trailing JSON: %w", err)
	}
	return value, nil
}

func normalizeStructuredOutput(value any, schema state.JSONSchema) (string, any, error) {
	issues := state.ValidateJSONSchemaValue(value, schema, "response")
	if len(issues) > 0 {
		return "", nil, NewExecutionError(ErrorInvalidOutput, state.NewValidationError("model response", issues).Error(), nil, map[string]any{"issues": issues})
	}
	normalized, err := json.Marshal(value)
	if err != nil {
		return "", nil, NewExecutionError(ErrorInvalidOutput, fmt.Sprintf("normalize structured output: %v", err), err, nil)
	}
	return string(normalized), value, nil
}

type compatibleJSONCandidate struct {
	content      string
	trailingSize int
	start        int
	priority     int
}

func compatibleJSONCandidates(content string) []string {
	candidates := fencedJSONCandidates(content)
	candidates = append(candidates, scannedJSONCandidates(content)...)
	sort.SliceStable(candidates, func(leftIndex, rightIndex int) bool {
		left := candidates[leftIndex]
		right := candidates[rightIndex]
		if left.priority != right.priority {
			return left.priority < right.priority
		}
		if left.trailingSize != right.trailingSize {
			return left.trailingSize < right.trailingSize
		}
		if len(left.content) != len(right.content) {
			return len(left.content) > len(right.content)
		}
		return left.start > right.start
	})
	result := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidate.content = strings.TrimSpace(candidate.content)
		if candidate.content == "" {
			continue
		}
		if _, exists := seen[candidate.content]; exists {
			continue
		}
		seen[candidate.content] = struct{}{}
		result = append(result, candidate.content)
	}
	return result
}

func fencedJSONCandidates(content string) []compatibleJSONCandidate {
	var candidates []compatibleJSONCandidate
	searchOffset := 0
	for searchOffset < len(content) {
		startRelative := strings.Index(content[searchOffset:], "```")
		if startRelative < 0 {
			break
		}
		start := searchOffset + startRelative
		headerStart := start + 3
		lineEndRelative := strings.IndexByte(content[headerStart:], '\n')
		if lineEndRelative < 0 {
			break
		}
		lineEnd := headerStart + lineEndRelative
		language := strings.TrimSpace(content[headerStart:lineEnd])
		bodyStart := lineEnd + 1
		endRelative := strings.Index(content[bodyStart:], "```")
		if endRelative < 0 {
			break
		}
		end := bodyStart + endRelative
		if language == "" || strings.EqualFold(language, "json") {
			candidates = append(candidates, compatibleJSONCandidate{
				content:      content[bodyStart:end],
				trailingSize: len(strings.TrimSpace(content[end+3:])),
				start:        start,
				priority:     0,
			})
		}
		searchOffset = end + 3
	}
	return candidates
}

func scannedJSONCandidates(content string) []compatibleJSONCandidate {
	candidates := make([]compatibleJSONCandidate, 0)
	for start := 0; start < len(content); start++ {
		if !isJSONValueStart(content[start]) || !hasJSONStartBoundary(content, start) {
			continue
		}
		end, value, ok := decodeJSONCandidatePrefix(content, start)
		if !ok {
			continue
		}
		if end <= start || !hasJSONEndBoundary(content, end, value) {
			continue
		}
		candidates = append(candidates, compatibleJSONCandidate{
			content:      content[start:end],
			trailingSize: len(strings.TrimSpace(content[end:])),
			start:        start,
			priority:     1,
		})
	}
	return candidates
}

func decodeJSONCandidatePrefix(content string, start int) (int, any, bool) {
	if start < 0 || start >= len(content) {
		return 0, nil, false
	}
	if content[start] == '-' || content[start] >= '0' && content[start] <= '9' {
		end, ok := jsonNumberEnd(content, start)
		if !ok {
			return 0, nil, false
		}
		value, err := decodeSingleJSONValue(content[start:end])
		return end, value, err == nil
	}
	switch content[start] {
	case 't':
		if strings.HasPrefix(content[start:], "true") {
			return start + len("true"), true, true
		}
	case 'f':
		if strings.HasPrefix(content[start:], "false") {
			return start + len("false"), false, true
		}
	case 'n':
		if strings.HasPrefix(content[start:], "null") {
			return start + len("null"), nil, true
		}
	}
	decoder := json.NewDecoder(strings.NewReader(content[start:]))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return 0, nil, false
	}
	return start + int(decoder.InputOffset()), value, true
}

func jsonNumberEnd(content string, start int) (int, bool) {
	index := start
	if content[index] == '-' {
		index++
		if index >= len(content) {
			return 0, false
		}
	}
	if content[index] == '0' {
		index++
	} else {
		if content[index] < '1' || content[index] > '9' {
			return 0, false
		}
		for index < len(content) && content[index] >= '0' && content[index] <= '9' {
			index++
		}
	}
	if index < len(content) && content[index] == '.' && index+1 < len(content) && content[index+1] >= '0' && content[index+1] <= '9' {
		index += 2
		for index < len(content) && content[index] >= '0' && content[index] <= '9' {
			index++
		}
	}
	if index < len(content) && (content[index] == 'e' || content[index] == 'E') {
		exponent := index + 1
		if exponent < len(content) && (content[exponent] == '+' || content[exponent] == '-') {
			exponent++
		}
		if exponent >= len(content) || content[exponent] < '0' || content[exponent] > '9' {
			return index, true
		}
		index = exponent + 1
		for index < len(content) && content[index] >= '0' && content[index] <= '9' {
			index++
		}
	}
	return index, true
}

func isJSONValueStart(value byte) bool {
	return value == '{' || value == '[' || value == '"' || value == '-' || value >= '0' && value <= '9' || value == 't' || value == 'f' || value == 'n'
}

func hasJSONStartBoundary(content string, index int) bool {
	if index == 0 {
		return true
	}
	previous := content[index-1]
	return !isASCIIWord(previous)
}

func hasJSONEndBoundary(content string, index int, value any) bool {
	if index >= len(content) {
		return true
	}
	switch value.(type) {
	case map[string]any, []any:
		return true
	default:
		return !isASCIIWord(content[index])
	}
}

func isASCIIWord(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_'
}

func cloneModelRequest(request llms.ModelRequest) (llms.ModelRequest, error) {
	stream := request.Stream
	request.Stream = nil
	clonedValue, err := cloneAnyValue(request)
	if err != nil {
		return llms.ModelRequest{}, fmt.Errorf("clone model request data: %w", err)
	}
	cloned, ok := clonedValue.(llms.ModelRequest)
	if !ok {
		return llms.ModelRequest{}, fmt.Errorf("clone model request returned %T", clonedValue)
	}
	cloned.Stream = stream
	return cloned, nil
}

func notifyModelCallObserver(ctx context.Context, observer ModelCallObserver, event ModelCallEvent) error {
	if observer == nil {
		return nil
	}
	clonedRequest, requestErr := cloneModelRequest(event.Request)
	if requestErr != nil {
		event.Request = llms.ModelRequest{}
		requestErr = fmt.Errorf("omit model observer request: %w", requestErr)
	} else {
		clonedRequest.Stream = nil
		event.Request = clonedRequest
	}
	clonedResponse, responseErr := cloneModelResponse(event.Response)
	event.Response = clonedResponse
	clonedErr, errorCloneErr := cloneObserverError(event.Err)
	event.Err = clonedErr
	event.CloneError = errors.Join(event.CloneError, requestErr, responseErr, errorCloneErr)
	return observer(ctx, event)
}

func cloneModelResponse(response *llms.ModelResponse) (*llms.ModelResponse, error) {
	if response == nil {
		return nil, nil
	}
	base := *response
	base.Metadata = nil
	if response.Choices != nil {
		base.Choices = make([]*llms.ModelChoice, len(response.Choices))
		for index, choice := range response.Choices {
			if choice == nil {
				continue
			}
			baseChoice := *choice
			baseChoice.Metadata = nil
			base.Choices[index] = &baseChoice
		}
	}
	clonedValue, err := cloneAnyValue(&base)
	if err != nil {
		return nil, fmt.Errorf("omit model observer response: %w", err)
	}
	cloned, ok := clonedValue.(*llms.ModelResponse)
	if !ok {
		return nil, fmt.Errorf("clone model response returned %T", clonedValue)
	}
	metadata := make(map[string]any, len(response.Choices)+1)
	if response.Metadata != nil {
		metadata["response"] = response.Metadata
	}
	for index, choice := range response.Choices {
		if choice != nil && choice.Metadata != nil {
			metadata[fmt.Sprintf("choice:%d", index)] = choice.Metadata
		}
	}
	if len(metadata) == 0 {
		return cloned, nil
	}
	clonedMetadataValue, err := cloneAnyValue(metadata)
	if err != nil {
		return cloned, fmt.Errorf("omit model observer metadata: %w", err)
	}
	clonedMetadata, _ := clonedMetadataValue.(map[string]any)
	if response.Metadata != nil {
		cloned.Metadata, _ = clonedMetadata["response"].(map[string]any)
	}
	for index, choice := range response.Choices {
		if choice == nil || choice.Metadata == nil {
			continue
		}
		cloned.Choices[index].Metadata, _ = clonedMetadata[fmt.Sprintf("choice:%d", index)].(map[string]any)
	}
	return cloned, nil
}

func newModelCallID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "model-call"
	}
	return hex.EncodeToString(buffer)
}
