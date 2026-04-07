package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFileOperationsWriteReadAndList(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv(fileToolWorkspaceEnv, workspace)

	writeOutput, err := fileOperationsTool(context.Background(), `{"action":"write","path":"notes/todo.txt","content":"hello world"}`)
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}

	var writeResp fileOperationResponse
	if err := json.Unmarshal([]byte(writeOutput), &writeResp); err != nil {
		t.Fatalf("unmarshal write response: %v", err)
	}
	if writeResp.Action != "write" || writeResp.Path != "notes/todo.txt" {
		t.Fatalf("unexpected write response: %#v", writeResp)
	}

	readOutput, err := fileOperationsTool(context.Background(), `{"action":"read","path":"notes/todo.txt"}`)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	var readResp fileOperationResponse
	if err := json.Unmarshal([]byte(readOutput), &readResp); err != nil {
		t.Fatalf("unmarshal read response: %v", err)
	}
	if readResp.Content != "hello world" {
		t.Fatalf("unexpected read content: %#v", readResp)
	}

	listOutput, err := fileOperationsTool(context.Background(), `{"action":"list","path":"notes"}`)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}

	var listResp fileOperationResponse
	if err := json.Unmarshal([]byte(listOutput), &listResp); err != nil {
		t.Fatalf("unmarshal list response: %v", err)
	}
	if len(listResp.Entries) != 1 || listResp.Entries[0].Path != "notes/todo.txt" {
		t.Fatalf("unexpected list response: %#v", listResp)
	}
}

func TestFileOperationsRejectPathEscape(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv(fileToolWorkspaceEnv, workspace)

	if _, err := fileOperationsTool(context.Background(), `{"action":"read","path":"../outside.txt"}`); err == nil {
		t.Fatal("expected path escape error")
	}
}

func TestFileOperationsUsesWorkspaceRoot(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv(fileToolWorkspaceEnv, workspace)

	if err := os.WriteFile(filepath.Join(workspace, "memo.txt"), []byte("note"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	output, err := fileOperationsTool(context.Background(), `{"action":"stat","path":"memo.txt"}`)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}

	var resp fileOperationResponse
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		t.Fatalf("unmarshal stat response: %v", err)
	}
	if resp.Workspace == "" || resp.Path != "memo.txt" || !resp.Exists {
		t.Fatalf("unexpected stat response: %#v", resp)
	}
}
