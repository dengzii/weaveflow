package node

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/dengzii/weaveflow/core"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
	"github.com/dengzii/weaveflow/state/accessors"
)

const (
	environmentToolWorkdirEnv         = "WEAVEFLOW_TOOL_WORKDIR"
	defaultEnvironmentGitStatusLimit  = 40
	environmentCommandTimeout         = 2 * time.Second
	environmentProjectReadPrefixBytes = 64 * 1024
)

type EnvironmentContextNode struct {
	Base
	EnvironmentStatePath string
	WorkspaceRoot        string
	IncludeGit           bool
	IncludeProject       bool
	GitStatusLimit       int
}

func NewEnvironmentContextNode(options ...NodeOption) *EnvironmentContextNode {
	node := &EnvironmentContextNode{
		Base: NewBase(Spec{
			Name:        NodeTypeEnvironmentContext,
			Description: "Collect deterministic workspace and project context for downstream agent nodes.",
			AccessorUses: []AccessorUse{
				UseRoot(accessors.EnvironmentID.Name()),
			},
		}),
		IncludeGit:     true,
		IncludeProject: true,
		GitStatusLimit: defaultEnvironmentGitStatusLimit,
	}
	applyNodeOptions(&node.Base, options)
	return node
}

func (n *EnvironmentContextNode) Execute(ctx core.Context, access *state.Access) error {
	path, err := n.environmentPath()
	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "environment.context.error", map[string]any{"error": err.Error()})
		return err
	}

	payload := n.collect(ctx)
	if err := access.SetAny(path, payload); err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "environment.context.error", map[string]any{"error": err.Error()})
		return err
	}

	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "environment.context", map[string]any{
		"environment_path": path.String(),
		"context":          payload,
	})
	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, map[string]any{
		"kind":             "environment_context",
		"environment_path": path.String(),
		"workspace_root":   payload["workspace_root"],
	})
	return nil
}

func (n *EnvironmentContextNode) Contract(registry *state.Registry) (state.Contract, error) {
	base, err := contractFromAccessorUses(registry, n.ID(), n.Scope(), n.Base.AccessorUses())
	if err != nil {
		return state.Contract{}, err
	}
	path, err := n.environmentPath()
	if err != nil {
		return state.Contract{}, err
	}
	fields := append([]state.FieldAccess(nil), base.Fields...)
	if path.String() != state.Shared(accessors.KeyEnvironment).String() {
		fields = append(fields, state.FieldAccess{
			Path:        path,
			Mode:        state.AccessWrite,
			Merge:       state.MergeReplace,
			Type:        "object",
			Description: "Collected workspace, project, and git context.",
		})
	}
	contract := state.NewContract(fields...)
	contract.WildcardRead = base.WildcardRead
	contract.WildcardWrite = base.WildcardWrite
	return contract, nil
}

func (n *EnvironmentContextNode) environmentPath() (state.Path, error) {
	text := strings.TrimSpace(n.EnvironmentStatePath)
	if text == "" {
		return state.Shared(accessors.KeyEnvironment), nil
	}
	return state.ParsePath(text)
}

func (n *EnvironmentContextNode) effectiveGitStatusLimit() int {
	if n == nil || n.GitStatusLimit <= 0 {
		return defaultEnvironmentGitStatusLimit
	}
	return n.GitStatusLimit
}

func (n *EnvironmentContextNode) collect(ctx context.Context) map[string]any {
	workspaceRoot, cwd, source := n.resolveWorkspaceRoot()
	payload := map[string]any{
		"workspace_root": workspaceRoot,
		"cwd":            cwd,
		"source":         source,
		"os":             goruntime.GOOS,
		"arch":           goruntime.GOARCH,
	}
	if shell := currentShell(); shell != "" {
		payload["shell"] = shell
	}
	if n.IncludeProject {
		if project := inspectProject(workspaceRoot); len(project) > 0 {
			payload["project"] = project
		}
	}
	if n.IncludeGit {
		if git := inspectGit(ctx, workspaceRoot, n.effectiveGitStatusLimit()); len(git) > 0 {
			payload["git"] = git
		}
	}
	return payload
}

func (n *EnvironmentContextNode) resolveWorkspaceRoot() (workspaceRoot string, cwd string, source string) {
	cwd = "."
	if dir, err := os.Getwd(); err == nil && strings.TrimSpace(dir) != "" {
		cwd = normalizeFilesystemPath(dir)
	}

	candidate := strings.TrimSpace(n.WorkspaceRoot)
	source = "config"
	if candidate == "" {
		candidate = strings.TrimSpace(os.Getenv(environmentToolWorkdirEnv))
		source = "tool_env"
	}
	if candidate == "" {
		candidate = cwd
		source = "process_cwd"
	}
	if abs, err := filepath.Abs(candidate); err == nil {
		candidate = abs
	}
	return normalizeFilesystemPath(candidate), cwd, source
}

func currentShell() string {
	if shell := strings.TrimSpace(os.Getenv("SHELL")); shell != "" {
		return shell
	}
	return strings.TrimSpace(os.Getenv("COMSPEC"))
}

func inspectProject(workspaceRoot string) map[string]any {
	project := map[string]any{}
	if moduleName := readGoModuleName(filepath.Join(workspaceRoot, "go.mod")); moduleName != "" {
		project["name"] = moduleName
		project["type"] = "go"
		project["manifest"] = "go.mod"
		project["test_command"] = "go test ./..."
	} else if packageInfo := readPackageJSON(filepath.Join(workspaceRoot, "package.json")); len(packageInfo) > 0 {
		for key, value := range packageInfo {
			project[key] = value
		}
		project["manifest"] = "package.json"
	}

	if title, summary := readReadmeSummary(workspaceRoot); title != "" || summary != "" {
		if _, ok := project["name"]; !ok && title != "" {
			project["name"] = title
		}
		if title != "" {
			project["readme_title"] = title
		}
		if summary != "" {
			project["summary"] = summary
		}
	}
	return project
}

func readGoModuleName(path string) string {
	content := readTextPrefix(path, environmentProjectReadPrefixBytes)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

func readPackageJSON(path string) map[string]any {
	content := readTextPrefix(path, environmentProjectReadPrefixBytes)
	if strings.TrimSpace(content) == "" {
		return nil
	}
	var raw struct {
		Name    string            `json:"name"`
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return nil
	}
	project := map[string]any{}
	if name := strings.TrimSpace(raw.Name); name != "" {
		project["name"] = name
	}
	project["type"] = "javascript"
	if test := strings.TrimSpace(raw.Scripts["test"]); test != "" {
		project["test_command"] = "npm test"
	}
	return project
}

func readReadmeSummary(workspaceRoot string) (title string, summary string) {
	for _, name := range []string{"README.md", "readme.md", "README"} {
		content := readTextPrefix(filepath.Join(workspaceRoot, name), environmentProjectReadPrefixBytes)
		if strings.TrimSpace(content) == "" {
			continue
		}
		return parseReadmeSummary(content)
	}
	return "", ""
}

func parseReadmeSummary(content string) (title string, summary string) {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if title == "" && strings.HasPrefix(line, "#") {
			title = strings.TrimSpace(strings.TrimLeft(line, "#"))
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		if summary == "" {
			summary = line
			break
		}
	}
	return title, summary
}

func inspectGit(ctx context.Context, workspaceRoot string, statusLimit int) map[string]any {
	root, err := runWorkspaceCommand(ctx, workspaceRoot, "git", "rev-parse", "--show-toplevel")
	if err != nil || strings.TrimSpace(root) == "" {
		return nil
	}

	git := map[string]any{
		"root": normalizeFilesystemPath(strings.TrimSpace(root)),
	}
	if branch, err := runWorkspaceCommand(ctx, workspaceRoot, "git", "branch", "--show-current"); err == nil {
		if branch = strings.TrimSpace(branch); branch != "" {
			git["branch"] = branch
		}
	}
	if commit, err := runWorkspaceCommand(ctx, workspaceRoot, "git", "rev-parse", "--short", "HEAD"); err == nil {
		if commit = strings.TrimSpace(commit); commit != "" {
			git["commit"] = commit
		}
	}
	if status, err := runWorkspaceCommand(ctx, workspaceRoot, "git", "status", "--short"); err == nil {
		lines := compactLines(status)
		git["dirty"] = len(lines) > 0
		git["changed_file_count"] = len(lines)
		if statusLimit <= 0 {
			statusLimit = defaultEnvironmentGitStatusLimit
		}
		if len(lines) > statusLimit {
			git["changed_files"] = append([]string(nil), lines[:statusLimit]...)
			git["changed_files_truncated"] = true
		} else if len(lines) > 0 {
			git["changed_files"] = lines
		}
	}
	return git
}

func runWorkspaceCommand(ctx context.Context, dir string, name string, args ...string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	execCtx, cancel := context.WithTimeout(ctx, environmentCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, name, args...)
	cmd.Dir = dir
	output, err := cmd.Output()
	if execCtx.Err() == context.DeadlineExceeded {
		return "", execCtx.Err()
	}
	if err != nil {
		return "", err
	}
	return string(output), nil
}

func readTextPrefix(path string, limit int64) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() {
		_ = file.Close()
	}()
	data, err := io.ReadAll(io.LimitReader(file, limit))
	if err != nil {
		return ""
	}
	return string(data)
}

func compactLines(text string) []string {
	lines := strings.Split(text, "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}

func normalizeFilesystemPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(path))
}
