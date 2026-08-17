package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/dengzii/weaveflow/internal/trigger"
	"github.com/dengzii/weaveflow/runtime"

	"github.com/gin-gonic/gin"
)

var (
	errGraphNotConfigured       = errors.New("graph is not configured")
	errRunnerNotConfigured      = errors.New("graph runner is not configured")
	errRegistryNotConfigured    = errors.New("registry is not configured")
	errEventStreamNotConfigured = errors.New("event stream is not configured")
	errInvalidGraphDefinition   = errors.New("invalid graph definition")
	errTriggerGraphNotFound     = errors.New("trigger graph not found")
	errTriggerPayloadRequired   = errors.New("trigger payload is required")
	errWebhookBodyTooLarge      = errors.New("webhook body is too large")
	errRequestBodyTooLarge      = errors.New("request body is too large")
	errGraphHeadConflict        = errors.New("graph head conflict")
	errGraphAlreadyExists       = errors.New("graph already exists")
)

type apiResponse struct {
	Data  any       `json:"data"`
	Error *apiError `json:"error,omitempty"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeData(c *gin.Context, status int, data any) {
	c.JSON(status, apiResponse{Data: data})
}

func writeError(c *gin.Context, status int, err error) {
	writeErrorData(c, status, err, nil)
}

func writeErrorData(c *gin.Context, status int, err error, data any) {
	message := "unknown error"
	if err != nil {
		message = err.Error()
	}
	c.JSON(status, apiResponse{
		Data: data,
		Error: &apiError{
			Code:    errorCode(status, err),
			Message: message,
		},
	})
}

func errorCode(status int, err error) string {
	switch {
	case errors.Is(err, errRequestBodyTooLarge), errors.Is(err, errWebhookBodyTooLarge):
		return "request_too_large"
	case errors.Is(err, errInvalidGraphDefinition):
		return "invalid_graph_definition"
	case errors.Is(err, errGraphHeadConflict):
		return "graph_head_conflict"
	case errors.Is(err, errGraphAlreadyExists):
		return "graph_exists"
	case errors.Is(err, trigger.ErrExists):
		return "trigger_id_conflict"
	case errors.Is(err, trigger.ErrDisabled):
		return "trigger_disabled"
	case errors.Is(err, runtime.ErrRunControlNotAllowed):
		return "run_control_not_allowed"
	case errors.Is(err, context.Canceled):
		return "request_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "request_timeout"
	}

	switch status {
	case http.StatusBadRequest:
		return "invalid_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusNotImplemented:
		return "not_supported"
	case http.StatusServiceUnavailable:
		return "service_unavailable"
	case http.StatusGatewayTimeout:
		return "request_timeout"
	default:
		return "internal_error"
	}
}

func statusForError(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, runtime.ErrRunnerRecordNotFound):
		return http.StatusNotFound
	case errors.Is(err, trigger.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, errTriggerGraphNotFound):
		return http.StatusNotFound
	case errors.Is(err, trigger.ErrExists), errors.Is(err, trigger.ErrBusy), errors.Is(err, trigger.ErrDisabled):
		return http.StatusConflict
	case errors.Is(err, errGraphHeadConflict), errors.Is(err, errGraphAlreadyExists):
		return http.StatusConflict
	case errors.Is(err, errRequestBodyTooLarge), errors.Is(err, errWebhookBodyTooLarge):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, errInvalidGraphDefinition):
		return http.StatusBadRequest
	case errors.Is(err, errInvalidRequest):
		return http.StatusBadRequest
	case errors.Is(err, runtime.ErrInvalidEventCursor):
		return http.StatusBadRequest
	case errors.Is(err, trigger.ErrInvalidTrigger), errors.Is(err, trigger.ErrInvalidPayload), errors.Is(err, trigger.ErrInvalidStateMapping), errors.Is(err, trigger.ErrInvalidTarget), errors.Is(err, trigger.ErrTypeMismatch):
		return http.StatusBadRequest
	case errors.Is(err, runtime.ErrRunControlNotAllowed):
		return http.StatusConflict
	case errors.Is(err, errGraphNotConfigured),
		errors.Is(err, errRunnerNotConfigured),
		errors.Is(err, errRegistryNotConfigured),
		errors.Is(err, errEventStreamNotConfigured):
		return http.StatusServiceUnavailable
	case errors.Is(err, context.Canceled):
		return http.StatusRequestTimeout
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout
	default:
		return http.StatusInternalServerError
	}
}

func statusForRequestError(err error) int {
	if errors.Is(err, errRequestBodyTooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

func readRequestBody(reader io.Reader, maxBytes int64) ([]byte, error) {
	if reader == nil {
		return nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%w: limit is %d bytes", errRequestBodyTooLarge, maxBytes)
	}
	return data, nil
}

func statusForListEventsError(err error) int {
	if err == nil {
		return http.StatusOK
	}
	text := err.Error()
	if strings.Contains(text, "does not support listing events") ||
		strings.Contains(text, "does not support listing") {
		return http.StatusNotImplemented
	}
	return statusForError(err)
}
