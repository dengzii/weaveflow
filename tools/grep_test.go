package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupGrepWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"main.go":                      "package main\n\nfunc helloWorld() {}\n",
		"cmd/cli/main.go":              "package cli\n\nfunc HelloWorld() {}\n",
		"internal/pkg/util.go":         "package pkg\n\nvar HelloWorld = 1\n",
		"docs/readme.md":               "# Hello World\nsome text\n",
		"node_modules/lodash/index.js": "helloWorld()\n",
		"binary.bin":                   "\x00\x01\x02HelloWorld\x00",
	}
	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	t.Setenv(fileToolWorkspaceEnv, root)
	return root
}

func runGrep(t *testing.T, req string) grepResponse {
	t.Helper()
	out, err := grepTool(context.Background(), req)
	if err != nil {
		t.Fatalf("grepTool: %v", err)
	}
	var resp grepResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp
}

func TestGrepFindsRegexMatches(t *testing.T) {
	setupGrepWorkspace(t)
	resp := runGrep(t, `{"pattern":"HelloWorld"}`)

	// Expect matches in main.go (none — case differs), cmd/cli/main.go, internal/pkg/util.go,
	// docs/readme.md (not match: "Hello World" has space)
	wantPaths := map[string]bool{
		"cmd/cli/main.go":      true,
		"internal/pkg/util.go": true,
	}
	gotPaths := map[string]bool{}
	for _, m := range resp.Matches {
		gotPaths[m.Path] = true
	}
	for p := range wantPaths {
		if !gotPaths[p] {
			t.Errorf("expected match in %s, got matches: %v", p, resp.Matches)
		}
	}
	if gotPaths["main.go"] {
		t.Errorf("did not expect match in main.go (it has helloWorld, not HelloWorld)")
	}
}

func TestGrepCaseInsensitive(t *testing.T) {
	setupGrepWorkspace(t)
	resp := runGrep(t, `{"pattern":"HelloWorld","case_insensitive":true}`)
	gotPaths := map[string]bool{}
	for _, m := range resp.Matches {
		gotPaths[m.Path] = true
	}
	if !gotPaths["main.go"] {
		t.Errorf("expected case-insensitive match in main.go, got matches: %v", resp.Matches)
	}
}

func TestGrepGlobFilter(t *testing.T) {
	setupGrepWorkspace(t)
	resp := runGrep(t, `{"pattern":"HelloWorld","glob":"**/*.go"}`)
	for _, m := range resp.Matches {
		if !strings.HasSuffix(m.Path, ".go") {
			t.Errorf("glob filter failed, got non-Go match: %s", m.Path)
		}
	}
}

func TestGrepSkipsBinaryFiles(t *testing.T) {
	setupGrepWorkspace(t)
	resp := runGrep(t, `{"pattern":"HelloWorld"}`)
	for _, m := range resp.Matches {
		if m.Path == "binary.bin" {
			t.Errorf("grep should skip binary files, got match in %s", m.Path)
		}
	}
}

func TestGrepSkipsIgnoredDirs(t *testing.T) {
	setupGrepWorkspace(t)
	resp := runGrep(t, `{"pattern":"helloWorld","case_insensitive":true}`)
	for _, m := range resp.Matches {
		if strings.HasPrefix(m.Path, "node_modules/") {
			t.Errorf("grep should skip node_modules, got match in %s", m.Path)
		}
	}
}

func TestGrepMaxResultsCap(t *testing.T) {
	root := setupGrepWorkspace(t)
	// Add many files to trigger the cap
	for i := 0; i < 50; i++ {
		full := filepath.Join(root, "many", "file"+strings.Repeat("x", i%10)+".go")
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte("HelloWorld\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	resp := runGrep(t, `{"pattern":"HelloWorld","max_results":5}`)
	if len(resp.Matches) > 5 {
		t.Fatalf("expected at most 5 matches, got %d", len(resp.Matches))
	}
	if !resp.Truncated {
		t.Fatalf("expected truncated=true when results capped")
	}
}

func TestGrepRejectsPathEscape(t *testing.T) {
	setupGrepWorkspace(t)
	if _, err := grepTool(context.Background(), `{"pattern":"x","path":"../etc"}`); err == nil {
		t.Fatalf("expected error for path escape")
	}
}

func TestGrepInvalidRegex(t *testing.T) {
	setupGrepWorkspace(t)
	if _, err := grepTool(context.Background(), `{"pattern":"["}`); err == nil {
		t.Fatalf("expected error for invalid regex")
	}
}

func TestGrepLineTruncation(t *testing.T) {
	root := setupGrepWorkspace(t)
	long := strings.Repeat("a", 500) + "MATCH" + strings.Repeat("b", 500)
	if err := os.WriteFile(filepath.Join(root, "long.go"), []byte(long), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	resp := runGrep(t, `{"pattern":"MATCH"}`)
	var found bool
	for _, m := range resp.Matches {
		if m.Path == "long.go" {
			found = true
			if len(m.Text) > maxGrepBytesPerLine+4 { // +4 for the ellipsis byte slack
				t.Errorf("expected truncated text, got len=%d", len(m.Text))
			}
		}
	}
	if !found {
		t.Errorf("did not find long.go match")
	}
}
