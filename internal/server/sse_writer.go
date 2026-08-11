package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

var (
	errSSEMarshal = errors.New("marshal SSE payload")
	errSSEWrite   = errors.New("write SSE frame")
	errSSEFlush   = errors.New("flush SSE frame")
)

func writeSSEJSON(writer http.ResponseWriter, eventName, eventID string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("%w: %w", errSSEMarshal, err)
	}
	if eventID != "" {
		if _, err := fmt.Fprintf(writer, "id: %s\n", sanitizeSSEField(eventID)); err != nil {
			return fmt.Errorf("%w: %w", errSSEWrite, err)
		}
	}
	if eventName != "" {
		if _, err := fmt.Fprintf(writer, "event: %s\n", sanitizeSSEField(eventName)); err != nil {
			return fmt.Errorf("%w: %w", errSSEWrite, err)
		}
	}
	if _, err := fmt.Fprintf(writer, "data: %s\n\n", data); err != nil {
		return fmt.Errorf("%w: %w", errSSEWrite, err)
	}
	if err := http.NewResponseController(writer).Flush(); err != nil {
		return fmt.Errorf("%w: %w", errSSEFlush, err)
	}
	return nil
}

func writeSSEComment(writer http.ResponseWriter, comment string) error {
	if _, err := fmt.Fprintf(writer, ": %s\n\n", sanitizeSSEField(comment)); err != nil {
		return fmt.Errorf("%w: %w", errSSEWrite, err)
	}
	if err := http.NewResponseController(writer).Flush(); err != nil {
		return fmt.Errorf("%w: %w", errSSEFlush, err)
	}
	return nil
}

func sseCloseReason(err error) string {
	switch {
	case errors.Is(err, errSSEMarshal):
		return "serialization_error"
	case errors.Is(err, errSSEFlush):
		return "transport_flush_error"
	case errors.Is(err, errSSEWrite):
		return "transport_write_error"
	default:
		return "stream_error"
	}
}
