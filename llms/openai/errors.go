package openai

import (
	"strings"

	"github.com/dengzii/weaveflow/core"
)

type errorMapping struct {
	patterns []string
	class    core.ErrorClass
	message  string
}

var errorMappings = []errorMapping{
	{patterns: []string{"incorrect api key", "invalid api key", "api key not found", "authentication"}, class: core.ErrorPermissionDenied, message: "invalid or missing API key"},
	{patterns: []string{"rate limit exceeded", "too many requests", "429"}, class: core.ErrorRateLimited, message: "model provider rate limit exceeded"},
	{patterns: []string{"model not found", "no such model", "invalid request", "400"}, class: core.ErrorInvalidInput, message: "model request is invalid"},
	{patterns: []string{"context length exceeded", "maximum context length", "quota exceeded", "billing hard limit"}, class: core.ErrorResourceExhausted, message: "model provider resource limit exceeded"},
	{patterns: []string{"content filtering", "content policy violation"}, class: core.ErrorNonRetryable, message: "model response was filtered by policy"},
	{patterns: []string{"service unavailable", "503", "bad gateway", "502"}, class: core.ErrorUnavailable, message: "model provider is unavailable"},
}

func MapError(err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	for _, mapping := range errorMappings {
		for _, pattern := range mapping.patterns {
			if strings.Contains(message, pattern) {
				return core.NewExecutionError(mapping.class, mapping.message, err, map[string]any{"provider": "openai"})
			}
		}
	}
	return core.NewExecutionError(core.ErrorUnknown, "model provider request failed", err, map[string]any{"provider": "openai"})
}
