package tools

import (
	"context"
	"strings"
	"testing"
)

func TestTreeProjectSize(t *testing.T) {
	SkipWorkspaceCheck = true
	t.Cleanup(func() { SkipWorkspaceCheck = false })

	tool := NewTree()
	out, err := tool.Handler(context.Background(), `{"path":"..","max_depth":3}`)
	if err != nil {
		t.Fatalf("tree: %v", err)
	}
	lines := strings.Count(out, "\n")
	t.Logf("project tree at depth 3 from tools/.. = %d lines, %d bytes", lines, len(out))
	if len(out) > 30000 {
		t.Errorf("tree output too large: %d bytes (target ≤ 30KB)", len(out))
	}
}
