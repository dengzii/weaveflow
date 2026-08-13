package tools

import (
	"context"
	"strings"
	"testing"
)

func TestOutlineSmoke_Go(t *testing.T) {
	t.Setenv(toolSkipWorkspaceCheckEnv, "true")

	tool := NewOutline()
	result, err := tool.Handler(context.Background(), toolCallForTest("outline", `{"file_path":"outline.go"}`))
	if err != nil {
		t.Fatalf("outline.go: %v", err)
	}
	out := result.Content
	checks := []string{"func NewOutline", "func outlineTool", "struct outlineEntry", "func formatGoFunc"}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("outline.go output missing %q\n---\n%s", want, out)
		}
	}
}
