package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type ErrorClass string

const (
	ErrorUnknown           ErrorClass = "unknown"
	ErrorInvalidInput      ErrorClass = "invalid_input"
	ErrorTimeout           ErrorClass = "timeout"
	ErrorCanceled          ErrorClass = "canceled"
	ErrorRateLimited       ErrorClass = "rate_limited"
	ErrorUnavailable       ErrorClass = "unavailable"
	ErrorPermissionDenied  ErrorClass = "permission_denied"
	ErrorSideEffectFailed  ErrorClass = "side_effect_failed"
	ErrorResourceExhausted ErrorClass = "resource_exhausted"
	ErrorNonRetryable      ErrorClass = "non_retryable"
)

type ExecutionError interface {
	error
	Class() ErrorClass
	RetryAfter() time.Duration
	Details() map[string]any
}

type ClassifiedError struct {
	class      ErrorClass
	message    string
	retryAfter time.Duration
	details    map[string]any
	cause      error
}

func NewExecutionError(class ErrorClass, message string, cause error, details map[string]any) *ClassifiedError {
	return &ClassifiedError{
		class:   normalizeErrorClass(class),
		message: strings.TrimSpace(message),
		details: cloneErrorDetails(details),
		cause:   cause,
	}
}

func (executionErr *ClassifiedError) WithRetryAfter(delay time.Duration) *ClassifiedError {
	if executionErr != nil {
		executionErr.retryAfter = max(delay, 0)
	}
	return executionErr
}

func (executionErr *ClassifiedError) Error() string {
	if executionErr == nil {
		return "execution failed"
	}
	if executionErr.message != "" {
		return executionErr.message
	}
	if executionErr.cause != nil {
		return executionErr.cause.Error()
	}
	return fmt.Sprintf("execution failed with class %q", executionErr.class)
}

func (executionErr *ClassifiedError) Unwrap() error {
	if executionErr == nil {
		return nil
	}
	return executionErr.cause
}

func (executionErr *ClassifiedError) Class() ErrorClass {
	if executionErr == nil {
		return ErrorUnknown
	}
	return normalizeErrorClass(executionErr.class)
}

func (executionErr *ClassifiedError) RetryAfter() time.Duration {
	if executionErr == nil {
		return 0
	}
	return executionErr.retryAfter
}

func (executionErr *ClassifiedError) Details() map[string]any {
	if executionErr == nil {
		return nil
	}
	return cloneErrorDetails(executionErr.details)
}

func ClassifyError(err error) ErrorClass {
	if err == nil {
		return ErrorUnknown
	}
	var executionErr ExecutionError
	if errors.As(err, &executionErr) {
		return normalizeErrorClass(executionErr.Class())
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return ErrorTimeout
	case errors.Is(err, context.Canceled):
		return ErrorCanceled
	default:
		return ErrorUnknown
	}
}

func IsRetryableErrorClass(class ErrorClass) bool {
	switch normalizeErrorClass(class) {
	case ErrorTimeout, ErrorRateLimited, ErrorUnavailable, ErrorSideEffectFailed:
		return true
	default:
		return false
	}
}

func normalizeErrorClass(class ErrorClass) ErrorClass {
	class = ErrorClass(strings.TrimSpace(string(class)))
	if class == "" {
		return ErrorUnknown
	}
	return class
}

func cloneErrorDetails(details map[string]any) map[string]any {
	if len(details) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(details))
	for key, value := range details {
		cloned[key] = value
	}
	return cloned
}
