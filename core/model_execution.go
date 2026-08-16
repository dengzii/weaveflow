package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	if len(request.ResponseSchema) == 0 {
		return nil
	}
	if len(response.Choices) == 0 || response.Choices[0] == nil {
		return NewExecutionError(ErrorNonRetryable, "structured model response has no choices", nil, nil)
	}
	content := strings.TrimSpace(response.Choices[0].Content)
	if content == "" {
		return NewExecutionError(ErrorNonRetryable, "structured model response is empty", nil, nil)
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return NewExecutionError(ErrorNonRetryable, fmt.Sprintf("structured model response is not valid JSON: %v", err), err, nil)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return NewExecutionError(ErrorNonRetryable, "structured model response contains multiple JSON values", nil, nil)
		}
		return NewExecutionError(ErrorNonRetryable, fmt.Sprintf("structured model response has trailing JSON: %v", err), err, nil)
	}
	issues := state.ValidateJSONSchemaValue(value, request.ResponseSchema, "response")
	if len(issues) > 0 {
		return NewExecutionError(ErrorNonRetryable, state.NewValidationError("model response", issues).Error(), nil, map[string]any{"issues": issues})
	}
	return nil
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
