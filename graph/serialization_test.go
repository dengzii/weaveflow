package graph

import (
	"path/filepath"
	"testing"
)

func TestResolveGraphFilePathPreservesAbsolutePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.json")
	resolved, err := resolveGraphFilePath(path)
	if err != nil {
		t.Fatalf("resolveGraphFilePath() error = %v", err)
	}
	if resolved != filepath.Clean(path) {
		t.Fatalf("resolveGraphFilePath() = %q, want %q", resolved, filepath.Clean(path))
	}
}
