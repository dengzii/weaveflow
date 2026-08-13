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
	request = cloneModelRequest(request)
	if request.CallID == "" {
		request.CallID = newModelCallID()
	}
	if request.Mode == "" {
		request.Mode = llms.ModelModeChat
	}
	if err := validateModelRequest(request); err != nil {
		return nil, err
	}

	observer, _ := ctx.Value(modelCallObserverKey{}).(ModelCallObserver)
	if observer != nil {
		if err := observer(ctx, ModelCallEvent{Stage: ModelCallStarted, Request: request}); err != nil {
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
				if err := observer(streamCtx, ModelCallEvent{Stage: ModelCallStream, Request: request, Stream: event}); err != nil {
					return err
				}
			}
			if originalStream != nil {
				return originalStream(streamCtx, event)
			}
			return nil
		}
	}

	response, err := model.Generate(ctx, request)
	if err != nil {
		if observer != nil {
			_ = observer(ctx, ModelCallEvent{Stage: ModelCallFailed, Request: request, Response: response, Err: err})
		}
		return response, err
	}
	if response == nil {
		err = errors.New("model returned a nil response")
		if observer != nil {
			_ = observer(ctx, ModelCallEvent{Stage: ModelCallFailed, Request: request, Err: err})
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
	if err := validateStructuredModelResponse(request, response); err != nil {
		if observer != nil {
			_ = observer(ctx, ModelCallEvent{Stage: ModelCallFailed, Request: request, Response: response, Err: err})
		}
		return response, err
	}
	if observer != nil {
		_ = observer(ctx, ModelCallEvent{Stage: ModelCallCompleted, Request: request, Response: response})
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

func cloneModelRequest(request llms.ModelRequest) llms.ModelRequest {
	request.Messages = llms.CloneMessages(request.Messages)
	request.Tools = append([]llms.ToolDefinition(nil), request.Tools...)
	request.StopWords = append([]string(nil), request.StopWords...)
	request.ResponseSchema = request.ResponseSchema.Clone()
	request.Metadata = cloneErrorDetails(request.Metadata)
	request.ProviderOptions = cloneErrorDetails(request.ProviderOptions)
	return request
}

func newModelCallID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "model-call"
	}
	return hex.EncodeToString(buffer)
}
