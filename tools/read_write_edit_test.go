package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupReadWriteEditWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv(fileToolWorkspaceEnv, root)
	return root
}

func TestReadToolReturnsNumberedLines(t *testing.T) {
	root := setupReadWriteEditWorkspace(t)
	path := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}

	out, err := readTool(context.Background(), `{"file_path":"notes.txt","offset":1,"limit":1}`)
	if err != nil {
		t.Fatalf("readTool: %v", err)
	}
	if !strings.Contains(out, "2\ttwo") {
		t.Fatalf("expected numbered second line, got:\n%s", out)
	}
	if strings.Contains(out, "1\tone") || strings.Contains(out, "3\tthree") {
		t.Fatalf("expected only requested line, got:\n%s", out)
	}
}

func TestWriteToolOverwritesFile(t *testing.T) {
	root := setupReadWriteEditWorkspace(t)
	path := filepath.Join(root, "notes.txt")

	out, err := writeTool(context.Background(), `{"file_path":"notes.txt","content":"new content"}`)
	if err != nil {
		t.Fatalf("writeTool: %v", err)
	}
	var resp fileOperationResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Path != "notes.txt" || resp.BytesWritten != len("new content") {
		t.Fatalf("unexpected response: %#v", resp)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(data) != "new content" {
		t.Fatalf("unexpected file content: %q", data)
	}
}

func TestEditToolReplacesUniqueString(t *testing.T) {
	root := setupReadWriteEditWorkspace(t)
	path := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}

	out, err := editTool(context.Background(), `{"file_path":"notes.txt","old_string":"world","new_string":"weaveflow"}`)
	if err != nil {
		t.Fatalf("editTool: %v", err)
	}
	var resp editResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Replacements != 1 {
		t.Fatalf("expected one replacement, got %#v", resp)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read edited file: %v", err)
	}
	if string(data) != "hello weaveflow\n" {
		t.Fatalf("unexpected file content: %q", data)
	}
}

func TestEditToolRejectsAmbiguousStringWithoutReplaceAll(t *testing.T) {
	root := setupReadWriteEditWorkspace(t)
	path := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(path, []byte("x x x"), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}

	if _, err := editTool(context.Background(), `{"file_path":"notes.txt","old_string":"x","new_string":"y"}`); err == nil {
		t.Fatal("expected ambiguity error")
	}
}

func TestEditToolReplaceAll(t *testing.T) {
	root := setupReadWriteEditWorkspace(t)
	path := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(path, []byte("x x x"), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}

	out, err := editTool(context.Background(), `{"file_path":"notes.txt","old_string":"x","new_string":"y","replace_all":true}`)
	if err != nil {
		t.Fatalf("editTool: %v", err)
	}
	var resp editResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Replacements != 3 {
		t.Fatalf("expected three replacements, got %#v", resp)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read edited file: %v", err)
	}
	if string(data) != "y y y" {
		t.Fatalf("unexpected file content: %q", data)
	}
}

func TestReadWriteEditRejectPathEscape(t *testing.T) {
	setupReadWriteEditWorkspace(t)
	if _, err := readTool(context.Background(), `{"file_path":"../outside.txt"}`); err == nil {
		t.Fatal("expected read path escape error")
	}
	if _, err := writeTool(context.Background(), `{"file_path":"../outside.txt","content":"x"}`); err == nil {
		t.Fatal("expected write path escape error")
	}
	if _, err := editTool(context.Background(), `{"file_path":"../outside.txt","old_string":"x","new_string":"y"}`); err == nil {
		t.Fatal("expected edit path escape error")
	}
}
