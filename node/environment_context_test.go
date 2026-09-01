package node

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

func TestEnvironmentContextCollectsConfiguredGoProject(t *testing.T) {
	t.Parallel()

	workspaceRoot := t.TempDir()
	writeEnvironmentTestFile(t, filepath.Join(workspaceRoot, "go.mod"), "module example.com/project\n\ngo 1.24\n")
	writeEnvironmentTestFile(t, filepath.Join(workspaceRoot, "README.md"), "# Example Project\n\nA focused project summary.\n")

	target := NewEnvironmentContextNode(WithID("environment"))
	target.WorkspaceRoot = workspaceRoot
	target.IncludeGit = false
	result, err := Execute(context.Background(), state.NewState(), target)
	if err != nil {
		t.Fatalf("execute environment context: %v", err)
	}
	raw, exists := state.NewAccess(result.State).ReadAny(target.EnvironmentPath)
	if !exists {
		t.Fatal("environment context was not written")
	}
	payload, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("environment context type = %T", raw)
	}
	if got := payload["workspace_root"]; got != normalizeFilesystemPath(workspaceRoot) {
		t.Fatalf("workspace root = %#v", got)
	}
	if payload["source"] != "config" || payload["os"] != runtime.GOOS || payload["arch"] != runtime.GOARCH {
		t.Fatalf("environment metadata = %#v", payload)
	}
	project, ok := payload["project"].(map[string]any)
	if !ok {
		t.Fatalf("project context = %#v", payload["project"])
	}
	if project["name"] != "example.com/project" || project["type"] != "go" || project["manifest"] != "go.mod" {
		t.Fatalf("project identity = %#v", project)
	}
	if project["readme_title"] != "Example Project" || project["summary"] != "A focused project summary." {
		t.Fatalf("project readme context = %#v", project)
	}
}

func TestEnvironmentContextUsesRuntimeEnvironment(t *testing.T) {
	t.Parallel()

	workspaceRoot := t.TempDir()
	target := NewEnvironmentContextNode(WithID("environment"))
	target.IncludeGit = false
	target.IncludeProject = false
	ctx := core.WithEnvironment(context.Background(), map[string]string{
		environmentToolWorkdirEnv: workspaceRoot,
		"SHELL":                   " /bin/custom ",
	})
	payload := target.collect(ctx)
	if payload["source"] != "runtime_environment" {
		t.Fatalf("workspace source = %#v", payload["source"])
	}
	if payload["workspace_root"] != normalizeFilesystemPath(workspaceRoot) {
		t.Fatalf("workspace root = %#v", payload["workspace_root"])
	}
	if payload["shell"] != "/bin/custom" {
		t.Fatalf("shell = %#v", payload["shell"])
	}
	if _, exists := payload["project"]; exists {
		t.Fatalf("project context unexpectedly collected: %#v", payload["project"])
	}
	if _, exists := payload["git"]; exists {
		t.Fatalf("git context unexpectedly collected: %#v", payload["git"])
	}
}

func TestEnvironmentContextDefinitionBuildsConfiguredNode(t *testing.T) {
	t.Parallel()

	target := NewEnvironmentContextNode(WithID("environment"))
	if err := target.Validate(); err != nil {
		t.Fatalf("validate default node: %v", err)
	}
	if got := target.EnvironmentPath.String(); got != "shared.environment" {
		t.Fatalf("default environment path = %q", got)
	}
	if got := target.Contract().Fields; len(got) != 1 || got[0].Path.String() != target.EnvironmentPath.String() {
		t.Fatalf("environment contract = %#v", got)
	}
	spec := target.GraphNodeSpec()
	if spec.Type != NodeTypeEnvironmentContext || spec.State["environment"].Path != "shared.environment" {
		t.Fatalf("graph node spec = %#v", spec)
	}

	environmentPath := state.Shared("runtime", "environment")
	built, err := EnvironmentContextNodeTypeDefinition().Build(&registry.BuildContext{}, registry.ResolvedNodeSpec{
		Spec: dsl.GraphNodeSpec{
			ID: "configured_environment",
			Config: map[string]any{
				"workspace_root":   " workspace ",
				"include_git":      false,
				"include_project":  false,
				"git_status_limit": 7,
			},
		},
		State: map[string]registry.ResolvedStateBinding{
			"environment": {Path: environmentPath},
		},
	})
	if err != nil {
		t.Fatalf("build environment context: %v", err)
	}
	environment := built.(*EnvironmentContextNode)
	if environment.EnvironmentPath.String() != environmentPath.String() || environment.WorkspaceRoot != "workspace" {
		t.Fatalf("built environment paths = %#v", environment)
	}
	if environment.IncludeGit || environment.IncludeProject || environment.GitStatusLimit != 7 {
		t.Fatalf("built environment config = %#v", environment)
	}

	_, err = EnvironmentContextNodeTypeDefinition().Build(&registry.BuildContext{}, registry.ResolvedNodeSpec{
		Spec: dsl.GraphNodeSpec{ID: "invalid", Config: map[string]any{"git_status_limit": 0}},
		State: map[string]registry.ResolvedStateBinding{
			"environment": {Path: environmentPath},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "must be greater than 0") {
		t.Fatalf("invalid git status limit error = %v", err)
	}

	var nilEnvironment *EnvironmentContextNode
	if err := nilEnvironment.Validate(); err == nil || !strings.Contains(err.Error(), "is nil") {
		t.Fatalf("nil validation error = %v", err)
	}
	if fields := nilEnvironment.Contract().Fields; len(fields) != 0 {
		t.Fatalf("nil contract fields = %#v", fields)
	}
	target.EnvironmentPath = state.Path{}
	if err := target.Validate(); err == nil || !strings.Contains(err.Error(), "requires environment path") {
		t.Fatalf("missing path validation error = %v", err)
	}
}

func TestInspectProjectRecognizesJavaScriptMetadata(t *testing.T) {
	t.Parallel()

	workspaceRoot := t.TempDir()
	writeEnvironmentTestFile(t, filepath.Join(workspaceRoot, "package.json"), `{
  "name": "web-project",
  "scripts": {"test": "bun test"}
}`)
	writeEnvironmentTestFile(t, filepath.Join(workspaceRoot, "readme.md"), "# Web Project\n\nFrontend summary.\n")

	project := inspectProject(workspaceRoot)
	if project["name"] != "web-project" || project["type"] != "javascript" || project["manifest"] != "package.json" {
		t.Fatalf("javascript project identity = %#v", project)
	}
	if project["test_command"] != "npm test" || project["readme_title"] != "Web Project" || project["summary"] != "Frontend summary." {
		t.Fatalf("javascript project metadata = %#v", project)
	}

	writeEnvironmentTestFile(t, filepath.Join(workspaceRoot, "package.json"), "not json")
	if got := readPackageJSON(filepath.Join(workspaceRoot, "package.json")); got != nil {
		t.Fatalf("invalid package metadata = %#v", got)
	}
}

func TestInspectGitReportsAndTruncatesChanges(t *testing.T) {
	t.Parallel()

	workspaceRoot := t.TempDir()
	if _, err := runWorkspaceCommand(context.Background(), workspaceRoot, "git", "init", "--quiet"); err != nil {
		t.Fatalf("initialize git repository: %v", err)
	}
	writeEnvironmentTestFile(t, filepath.Join(workspaceRoot, "first.txt"), "first")
	writeEnvironmentTestFile(t, filepath.Join(workspaceRoot, "second.txt"), "second")

	git := inspectGit(context.Background(), workspaceRoot, 1)
	if git == nil {
		t.Fatal("git context was not collected")
	}
	if git["root"] != normalizeFilesystemPath(workspaceRoot) || git["dirty"] != true {
		t.Fatalf("git context = %#v", git)
	}
	if git["changed_file_count"] != 2 || git["changed_files_truncated"] != true {
		t.Fatalf("git change summary = %#v", git)
	}
	changedFiles, ok := git["changed_files"].([]string)
	if !ok || len(changedFiles) != 1 {
		t.Fatalf("changed files = %#v", git["changed_files"])
	}

	if got := inspectGit(context.Background(), filepath.Join(workspaceRoot, "missing"), 1); got != nil {
		t.Fatalf("non-repository git context = %#v", got)
	}
}

func TestEnvironmentContextTextHelpers(t *testing.T) {
	t.Parallel()

	title, summary := parseReadmeSummary("\n## Title\n\n### Details\nA summary line.\nAnother line.\n")
	if title != "Title" || summary != "A summary line." {
		t.Fatalf("readme title=%q summary=%q", title, summary)
	}
	if got := compactLines(" first \n\n second\r\n "); len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("compact lines = %#v", got)
	}
	if got := normalizeFilesystemPath("  alpha/../beta  "); got != "beta" {
		t.Fatalf("normalized path = %q", got)
	}
	if got := normalizeFilesystemPath("  "); got != "" {
		t.Fatalf("empty normalized path = %q", got)
	}

	path := filepath.Join(t.TempDir(), "prefix.txt")
	writeEnvironmentTestFile(t, path, "abcdef")
	if got := readTextPrefix(path, 3); got != "abc" {
		t.Fatalf("text prefix = %q", got)
	}
	if got := readTextPrefix(path+".missing", 3); got != "" {
		t.Fatalf("missing text prefix = %q", got)
	}
}

func writeEnvironmentTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
