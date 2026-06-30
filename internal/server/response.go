package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/dengzii/weaveflow/runtime"

	"github.com/gin-gonic/gin"
)

var (
	errGraphNotConfigured       = errors.New("graph is not configured")
	errRunnerNotConfigured      = errors.New("graph runner is not configured")
	errRegistryNotConfigured    = errors.New("registry is not configured")
	errEventStreamNotConfigured = errors.New("event stream is not configured")
)

type apiResponse struct {
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
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
	c.JSON(status, apiResponse{Data: data, Error: message})
}

func statusForError(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, runtime.ErrRunnerRecordNotFound):
		return http.StatusNotFound
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
