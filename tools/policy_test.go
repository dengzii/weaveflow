package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/core"
)

func TestSecureWorkspacePathRejectsEscapeAndSymlink(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(workspace, "outside")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	outsideFile := filepath.Join(outside, "target.txt")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o600); err != nil {
		t.Fatalf("outside file: %v", err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(workspace, "outside-file")); err != nil {
		t.Fatalf("file symlink: %v", err)
	}
	for _, path := range []string{"../escape.txt", filepath.Join("outside", "escape.txt"), "outside-file"} {
		if _, err := SecureWorkspacePath(workspace, path, false); err == nil {
			t.Fatalf("path %q was accepted", path)
		}
	}
}

func TestGuardWorkspaceToolEnforcesAllowlistAndDefaultSearchRoot(t *testing.T) {
	workspace := t.TempDir()
	allowed := filepath.Join(workspace, "allowed")
	blocked := filepath.Join(workspace, "blocked")
	if err := os.MkdirAll(allowed, 0o755); err != nil {
		t.Fatalf("allowed directory: %v", err)
	}
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatalf("blocked directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(allowed, "inside.txt"), []byte("inside"), 0o600); err != nil {
		t.Fatalf("allowed fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "outside.txt"), []byte("outside"), 0o600); err != nil {
		t.Fatalf("blocked fixture: %v", err)
	}
	allowedPaths, err := ResolveAllowedPaths(workspace, []string{"allowed"})
	if err != nil {
		t.Fatalf("resolve allowlist: %v", err)
	}
	read := GuardWorkspaceTool(NewRead(), workspace, allowedPaths)
	glob := GuardWorkspaceTool(NewGlob(), workspace, allowedPaths)
	ctx := core.WithEnvironment(context.Background(), map[string]string{toolWorkspaceEnv: workspace})
	ctx = core.WithToolPermissions(ctx, "filesystem.read")
	if _, err := core.ExecuteTool(ctx, read, toolCallForTest("read", `{"file_path":"blocked/outside.txt"}`)); err == nil {
		t.Fatal("read outside allowlist was accepted")
	}
	if _, err := core.ExecuteTool(ctx, read, toolCallForTest("read", `{"file_path":"allowed/inside.txt"}`)); err != nil {
		t.Fatalf("read inside allowlist: %v", err)
	}
	otherWorkspace := t.TempDir()
	mismatchedContext := core.WithEnvironment(context.Background(), map[string]string{toolWorkspaceEnv: otherWorkspace})
	mismatchedContext = core.WithToolPermissions(mismatchedContext, "filesystem.read")
	if _, err := core.ExecuteTool(mismatchedContext, read, toolCallForTest("read", `{"file_path":"allowed/inside.txt"}`)); err != nil {
		t.Fatalf("read did not use guarded workspace: %v", err)
	}
	if _, err := core.ExecuteTool(ctx, glob, toolCallForTest("glob", `{"pattern":"*.txt"}`)); err == nil {
		t.Fatal("unscoped glob was accepted")
	}
}

func TestWithReadLimitsBoundsSchemaAndOutput(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "long.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	read := WithReadLimits(NewRead(), 2, 64)
	properties := read.Function.Parameters["properties"].(map[string]any)
	limitSchema := properties["limit"].(map[string]any)
	if limitSchema["maximum"] != 2 {
		t.Fatalf("limit schema = %#v", limitSchema)
	}
	ctx := core.WithEnvironment(context.Background(), map[string]string{toolWorkspaceEnv: workspace})
	ctx = core.WithToolPermissions(ctx, "filesystem.read")
	result, err := core.ExecuteTool(ctx, read, toolCallForTest("read", `{"file_path":"long.txt"}`))
	if err != nil {
		t.Fatalf("bounded read: %v", err)
	}
	if !strings.Contains(result.Content, "2\ttwo") || strings.Contains(result.Content, "3\tthree") {
		t.Fatalf("bounded read output = %q", result.Content)
	}
	if _, err := core.ExecuteTool(ctx, read, toolCallForTest("read", `{"file_path":"long.txt","limit":3}`)); err == nil {
		t.Fatal("read accepted a limit above the configured maximum")
	}
}

func TestWithReadLimitsCanOnlyBoundOutput(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "long.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	read := WithReadLimits(NewRead(), 0, 8)
	ctx := core.WithEnvironment(context.Background(), map[string]string{toolWorkspaceEnv: workspace})
	ctx = core.WithToolPermissions(ctx, "filesystem.read")
	result, err := core.ExecuteTool(ctx, read, toolCallForTest("read", `{"file_path":"long.txt","limit":3}`))
	if err != nil {
		t.Fatalf("output-only bound: %v", err)
	}
	if !strings.Contains(result.Content, "truncated by tool output limit") {
		t.Fatalf("output-only bound result = %q", result.Content)
	}
}
