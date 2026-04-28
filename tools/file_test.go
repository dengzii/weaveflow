package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFileReadAndWrite(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv(fileToolWorkspaceEnv, workspace)

	writeOutput, err := fileWriteTool(context.Background(), `{"action":"write","path":"notes/todo.txt","content":"hello world"}`)
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

	readOutput, err := fileReadTool(context.Background(), `{"action":"read","path":"notes/todo.txt"}`)
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

	listOutput, err := fileReadTool(context.Background(), `{"action":"list","path":"notes"}`)
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

func TestFileReadRejectPathEscape(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv(fileToolWorkspaceEnv, workspace)

	if _, err := fileReadTool(context.Background(), `{"action":"read","path":"../outside.txt"}`); err == nil {
		t.Fatal("expected path escape error")
	}
}

func TestFileWriteRejectPathEscape(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv(fileToolWorkspaceEnv, workspace)

	if _, err := fileWriteTool(context.Background(), `{"action":"write","path":"../outside.txt","content":"bad"}`); err == nil {
		t.Fatal("expected path escape error")
	}
}

func TestFileReadUsesWorkspaceRoot(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv(fileToolWorkspaceEnv, workspace)

	if err := os.WriteFile(filepath.Join(workspace, "memo.txt"), []byte("note"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	output, err := fileReadTool(context.Background(), `{"action":"stat","path":"memo.txt"}`)
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

func TestFileReadRejectsWriteAction(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv(fileToolWorkspaceEnv, workspace)

	if _, err := fileReadTool(context.Background(), `{"action":"write","path":"test.txt","content":"x"}`); err == nil {
		t.Fatal("expected error for write action on file_read tool")
	}
}

func TestFileWriteRejectsReadAction(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv(fileToolWorkspaceEnv, workspace)

	os.WriteFile(filepath.Join(workspace, "test.txt"), []byte("x"), 0o644)

	if _, err := fileWriteTool(context.Background(), `{"action":"read","path":"test.txt"}`); err == nil {
		t.Fatal("expected error for read action on file_write tool")
	}
}
