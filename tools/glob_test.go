package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func setupGlobWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := []string{
		"main.go",
		"cmd/cli/main.go",
		"cmd/server/server.go",
		"internal/pkg/util.go",
		"internal/pkg/util_test.go",
		"docs/readme.md",
		"node_modules/lodash/index.js",
		".git/config",
		"vendor/dep/dep.go",
	}
	for _, rel := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	t.Setenv(fileToolWorkspaceEnv, root)
	return root
}

func runGlob(t *testing.T, req string) globResponse {
	t.Helper()
	out, err := globTool(context.Background(), req)
	if err != nil {
		t.Fatalf("globTool: %v", err)
	}
	var resp globResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp
}

func TestGlobDoubleStarRecursive(t *testing.T) {
	setupGlobWorkspace(t)
	resp := runGlob(t, `{"pattern":"**/*.go"}`)
	sort.Strings(resp.Paths)
	got := strings.Join(resp.Paths, ",")
	want := "cmd/cli/main.go,cmd/server/server.go,internal/pkg/util.go,internal/pkg/util_test.go,main.go"
	if got != want {
		t.Fatalf("paths mismatch:\n got %s\nwant %s", got, want)
	}
}

func TestGlobSkipsIgnoredDirs(t *testing.T) {
	setupGlobWorkspace(t)
	resp := runGlob(t, `{"pattern":"**/*"}`)
	for _, p := range resp.Paths {
		if strings.HasPrefix(p, "node_modules/") ||
			strings.HasPrefix(p, ".git/") ||
			strings.HasPrefix(p, "vendor/") {
			t.Fatalf("unexpected path included from skip dir: %s", p)
		}
	}
}

func TestGlobScopedRoot(t *testing.T) {
	setupGlobWorkspace(t)
	resp := runGlob(t, `{"pattern":"**/*.go","path":"cmd"}`)
	sort.Strings(resp.Paths)
	want := []string{"cmd/cli/main.go", "cmd/server/server.go"}
	if strings.Join(resp.Paths, ",") != strings.Join(want, ",") {
		t.Fatalf("scoped paths mismatch: got %v want %v", resp.Paths, want)
	}
}

func TestGlobMaxResultsCap(t *testing.T) {
	setupGlobWorkspace(t)
	resp := runGlob(t, `{"pattern":"**/*.go","max_results":2}`)
	if len(resp.Paths) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(resp.Paths))
	}
	if !resp.Truncated {
		t.Fatalf("expected truncated=true when results capped")
	}
}

func TestGlobRejectsPathEscape(t *testing.T) {
	setupGlobWorkspace(t)
	if _, err := globTool(context.Background(), `{"pattern":"**/*","path":"../etc"}`); err == nil {
		t.Fatalf("expected error for path escape, got nil")
	}
}

func TestGlobMissingPatternErrors(t *testing.T) {
	setupGlobWorkspace(t)
	if _, err := globTool(context.Background(), `{"path":"."}`); err == nil {
		t.Fatalf("expected error for missing pattern")
	}
}

func TestMatchGlobSegmentMatching(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"**/*.go", "main.go", true},
		{"**/*.go", "a/b/c.go", true},
		{"*.go", "main.go", true},
		{"*.go", "a/main.go", false},
		{"cmd/**/*.go", "cmd/cli/main.go", true},
		{"cmd/**/*.go", "internal/x.go", false},
		{"internal/**/*_test.go", "internal/pkg/util_test.go", true},
	}
	for _, c := range cases {
		if got := matchGlob(c.pattern, c.path); got != c.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}
