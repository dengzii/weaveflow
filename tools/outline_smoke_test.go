package tools

import (
	"context"
	"strings"
	"testing"
)

func TestOutlineSmoke_Go(t *testing.T) {
	SkipWorkspaceCheck = true
	t.Cleanup(func() { SkipWorkspaceCheck = false })

	tool := NewOutline()
	out, err := tool.Handler(context.Background(), `{"file_path":"outline.go"}`)
	if err != nil {
		t.Fatalf("outline.go: %v", err)
	}
	checks := []string{"func NewOutline", "func outlineTool", "struct outlineEntry", "func formatGoFunc"}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("outline.go output missing %q\n---\n%s", want, out)
		}
	}
}
