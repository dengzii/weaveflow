package redact

import (
	"strings"
	"testing"
)

func TestJSONString(t *testing.T) {
	t.Parallel()

	raw := `{"api_key":"secret-value","nested":{"authorization":"Bearer abc"}}`
	redacted := JSONString(raw)

	if strings.Contains(redacted, "secret-value") || strings.Contains(redacted, "Bearer abc") {
		t.Fatalf("expected sensitive values to be redacted, got %s", redacted)
	}
	if !strings.Contains(redacted, Placeholder) {
		t.Fatalf("expected redacted placeholder, got %s", redacted)
	}
}

func TestPath(t *testing.T) {
	t.Parallel()

	if got := Path(`E:\models\demo.gguf`); got != "demo.gguf" {
		t.Fatalf("unexpected redacted path %q", got)
	}
}
