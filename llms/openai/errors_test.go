package openai

import (
	"errors"
	"testing"

	"github.com/dengzii/weaveflow/core"
)

func TestMapErrorClassifiesTransportFailures(t *testing.T) {
	tests := []struct {
		message string
		class   core.ErrorClass
	}{
		{message: "request timeout: API call exceeded deadline", class: core.ErrorTimeout},
		{message: "network error: failed to reach API server", class: core.ErrorUnavailable},
		{message: "API returned unexpected status code: 500", class: core.ErrorUnavailable},
		{message: "API returned unexpected status code: 504", class: core.ErrorUnavailable},
		{message: "request cancelled", class: core.ErrorCanceled},
	}
	for _, test := range tests {
		t.Run(test.message, func(t *testing.T) {
			if got := core.ClassifyError(MapError(errors.New(test.message))); got != test.class {
				t.Fatalf("MapError() class = %q, want %q", got, test.class)
			}
		})
	}
}
