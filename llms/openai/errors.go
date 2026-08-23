package openai

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/llms/openai/internal/openaiclient"
)

type errorMapping struct {
	patterns []string
	class    core.ErrorClass
	message  string
}

var errorMappings = []errorMapping{
	{patterns: []string{"incorrect api key", "invalid api key", "api key not found", "authentication"}, class: core.ErrorPermissionDenied, message: "invalid or missing API key"},
	{patterns: []string{"rate limit exceeded", "too many requests", "429"}, class: core.ErrorRateLimited, message: "model provider rate limit exceeded"},
	{patterns: []string{"request timeout", "deadline exceeded", "status code: 408"}, class: core.ErrorTimeout, message: "model provider request timed out"},
	{patterns: []string{"model not found", "no such model", "invalid request", "400"}, class: core.ErrorInvalidInput, message: "model request is invalid"},
	{patterns: []string{"context length exceeded", "maximum context length", "quota exceeded", "billing hard limit"}, class: core.ErrorResourceExhausted, message: "model provider resource limit exceeded"},
	{patterns: []string{"content filtering", "content policy violation"}, class: core.ErrorNonRetryable, message: "model response was filtered by policy"},
	{patterns: []string{"service unavailable", "network error", "failed to reach api server", "connection reset", "unexpected eof", "status code: 500", "status code: 502", "status code: 503", "status code: 504", "bad gateway"}, class: core.ErrorUnavailable, message: "model provider is unavailable"},
	{patterns: []string{"request cancelled", "request canceled"}, class: core.ErrorCanceled, message: "model provider request was canceled"},
}

func MapError(err error) error {
	return mapProviderError(err, providerErrorContext{provider: ProviderOpenAI})
}

type providerErrorContext struct {
	provider  Provider
	model     string
	operation string
	apiFormat APIFormat
}

func (o *LLM) mapError(operation, model string, err error) error {
	provider := ProviderOpenAI
	apiFormat := APIFormatChatCompletions
	if o != nil {
		provider = o.provider
		apiFormat = o.apiFormat
	}
	return mapProviderError(err, providerErrorContext{
		provider:  provider,
		model:     strings.TrimSpace(model),
		operation: strings.TrimSpace(operation),
		apiFormat: apiFormat,
	})
}

func mapProviderError(err error, requestContext providerErrorContext) error {
	if err == nil {
		return nil
	}
	class, summary := classifyProviderError(err)
	diagnostic := strings.TrimSpace(err.Error())
	message := providerErrorPrefix(requestContext)
	if diagnostic != "" {
		message += ": " + diagnostic
	}
	details := providerErrorDetails(err, requestContext)
	if summary != "" {
		details["summary"] = summary
	}
	return core.NewExecutionError(class, message, err, details)
}

func classifyProviderError(err error) (core.ErrorClass, string) {
	if errors.Is(err, context.DeadlineExceeded) {
		return core.ErrorTimeout, "model provider request timed out"
	}
	if errors.Is(err, context.Canceled) {
		return core.ErrorCanceled, "model provider request was canceled"
	}

	message := strings.ToLower(err.Error())
	for _, mapping := range errorMappings {
		for _, pattern := range mapping.patterns {
			if strings.Contains(message, pattern) {
				return mapping.class, mapping.message
			}
		}
	}

	var statusErr *openaiclient.HTTPStatusError
	if errors.As(err, &statusErr) {
		switch statusErr.StatusCode {
		case 400, 404, 409, 422:
			return core.ErrorInvalidInput, "model request is invalid"
		case 401, 403:
			return core.ErrorPermissionDenied, "model provider denied the request"
		case 408:
			return core.ErrorTimeout, "model provider request timed out"
		case 429:
			return core.ErrorRateLimited, "model provider rate limit exceeded"
		case 500, 502, 503, 504:
			return core.ErrorUnavailable, "model provider is unavailable"
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return core.ErrorUnavailable, "model provider network request failed"
	}
	return core.ErrorUnknown, "model provider request failed"
}

func providerErrorPrefix(requestContext providerErrorContext) string {
	provider := strings.TrimSpace(string(requestContext.provider))
	if provider == "" {
		provider = "model provider"
	}
	operation := strings.TrimSpace(requestContext.operation)
	if operation == "" {
		operation = "model"
	}
	message := provider + " " + operation + " request failed"
	if model := strings.TrimSpace(requestContext.model); model != "" {
		message += fmt.Sprintf(" for model %q", model)
	}
	return message
}

func providerErrorDetails(err error, requestContext providerErrorContext) map[string]any {
	details := map[string]any{
		"provider":       strings.TrimSpace(string(requestContext.provider)),
		"provider_error": strings.TrimSpace(err.Error()),
	}
	if model := strings.TrimSpace(requestContext.model); model != "" {
		details["model"] = model
	}
	if operation := strings.TrimSpace(requestContext.operation); operation != "" {
		details["operation"] = operation
	}
	if apiFormat := strings.TrimSpace(string(requestContext.apiFormat)); apiFormat != "" {
		details["api_format"] = apiFormat
	}

	var statusErr *openaiclient.HTTPStatusError
	if !errors.As(err, &statusErr) {
		return details
	}
	details["status_code"] = statusErr.StatusCode
	if statusErr.ProviderMessage != "" {
		details["provider_message"] = statusErr.ProviderMessage
	}
	if statusErr.ProviderType != "" {
		details["provider_error_type"] = statusErr.ProviderType
	}
	if statusErr.ProviderCode != "" {
		details["provider_error_code"] = statusErr.ProviderCode
	}
	if statusErr.ProviderParam != "" {
		details["provider_error_param"] = statusErr.ProviderParam
	}
	if statusErr.RequestID != "" {
		details["provider_request_id"] = statusErr.RequestID
	}
	return details
}
