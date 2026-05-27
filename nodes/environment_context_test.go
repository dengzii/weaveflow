package nodes

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	wfstate "weaveflow/state"
)

func TestEnvironmentContextNodeCollectsWorkspaceProjectContext(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module example.com/demo\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("# Demo Project\n\nA small project used by tests.\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}

	node := NewEnvironmentContextNode()
	node.WorkspaceRoot = workspace
	node.IncludeGit = false

	next, err := runTestNode(t, node, context.Background(), wfstate.State{})
	if err != nil {
		t.Fatalf("invoke environment context: %v", err)
	}

	environment := next.Get(wfstate.StateKeyEnvironment)
	if environment == nil {
		t.Fatal("expected environment state")
	}
	if got := environment["workspace_root"]; got != filepath.ToSlash(filepath.Clean(workspace)) {
		t.Fatalf("workspace_root = %#v, want %q", got, filepath.ToSlash(filepath.Clean(workspace)))
	}
	if got := environment["source"]; got != "config" {
		t.Fatalf("source = %#v, want config", got)
	}

	project := asStringMap(environment["project"])
	if project == nil {
		t.Fatalf("expected project context, got %#v", environment["project"])
	}
	if project["name"] != "example.com/demo" {
		t.Fatalf("project name = %#v, want module name", project["name"])
	}
	if project["type"] != "go" || project["test_command"] != "go test ./..." {
		t.Fatalf("unexpected project context: %#v", project)
	}
	if project["summary"] != "A small project used by tests." {
		t.Fatalf("summary = %#v", project["summary"])
	}
	if _, ok := environment["git"]; ok {
		t.Fatalf("expected git context to be disabled, got %#v", environment["git"])
	}
}
