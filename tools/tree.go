package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dengzii/weaveflow/llms"
)

const (
	defaultTreeMaxDepth   = 3
	defaultTreeMaxEntries = 500
	maxTreeMaxDepth       = 6
	maxTreeMaxEntries     = 2000
)

// treeSkipDirs is the always-skipped set: VCS metadata, dependency caches, build
// output, IDE caches. Workers exploring a scope can still access these via
// outline/read if explicitly requested.
var treeSkipDirs = map[string]struct{}{
	".git": {}, ".svn": {}, ".hg": {}, ".idea": {}, ".vscode": {}, ".vs": {},
	"node_modules": {}, "bower_components": {}, "vendor": {},
	"target": {}, "build": {}, "dist": {}, "out": {}, "bin": {}, "obj": {},
	".next": {}, ".nuxt": {}, ".cache": {}, ".gradle": {}, ".mvn": {},
	"__pycache__": {}, ".pytest_cache": {}, ".mypy_cache": {}, ".ruff_cache": {},
	"coverage": {}, ".nyc_output": {}, ".terraform": {},
}

type treeRequest struct {
	Path       string `json:"path,omitempty"`
	MaxDepth   int    `json:"max_depth,omitempty"`
	MaxEntries int    `json:"max_entries,omitempty"`
}

// NewTree returns a tool that prints a depth-bounded indented directory tree of
// the project, skipping common dependency/build/cache directories. Use it once
// at the start of a session to get an overview of the project layout.
func NewTree() Tool {
	return Tool{
		Function: &llms.FunctionDefinition{
			Name: "tree",
			Description: "Return a depth-bounded indented directory tree of a path " +
				"(defaults: project root, max_depth=3, max_entries=500). Skips VCS " +
				"metadata, node_modules, vendor, build/dist/target, IDE caches, etc. " +
				"Use this once for project overview instead of calling glob multiple times.",
			OutputSchema: textOutputSchema(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Root to walk. Defaults to workspace root.",
					},
					"max_depth": map[string]any{
						"type":             "integer",
						"exclusiveMinimum": 0,
						"description":      "Maximum directory depth. Defaults to 3, capped at 6.",
					},
					"max_entries": map[string]any{
						"type":             "integer",
						"exclusiveMinimum": 0,
						"description":      "Maximum total entries (files+dirs) to emit. Defaults to 500, capped at 2000.",
					},
				},
				"additionalProperties": false,
			},
		},
		Handler:     treeTool,
		Permissions: []string{"filesystem.read"},
	}
}

func treeTool(ctx context.Context, call llms.ToolCall) (llms.ToolResult, error) {
	var req treeRequest
	if err := decodeToolArguments(call, &req); err != nil {
		return llms.ToolResult{}, fmt.Errorf("tree input: %w", err)
	}
	if strings.TrimSpace(req.Path) == "" {
		req.Path = "."
	}
	depth := normalizeTreeDepth(req.MaxDepth)
	maxEntries := normalizeTreeMaxEntries(req.MaxEntries)

	_, target, relativePath, err := resolveToolPath(ctx, req.Path)
	if err != nil {
		return llms.ToolResult{}, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return llms.ToolResult{}, err
	}
	if !info.IsDir() {
		return llms.ToolResult{}, fmt.Errorf("tree path must be a directory")
	}

	var b strings.Builder
	if relativePath == "." || relativePath == "" {
		b.WriteString("./\n")
	} else {
		fmt.Fprintf(&b, "%s/\n", strings.TrimSuffix(relativePath, "/"))
	}
	emitted := 0
	truncated := false
	if err := walkTree(target, "", 0, depth, maxEntries, &emitted, &truncated, &b); err != nil {
		return llms.ToolResult{}, err
	}
	if truncated {
		fmt.Fprintf(&b, "[truncated: %d entries cap reached; raise max_entries or narrow path]\n", maxEntries)
	}
	return textToolResult(call, b.String()), nil
}

func walkTree(root string, indent string, depth int, maxDepth int, maxEntries int, emitted *int, truncated *bool, b *strings.Builder) error {
	if depth >= maxDepth {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	dirs := make([]os.DirEntry, 0, len(entries))
	files := make([]os.DirEntry, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") && name != "." && name != ".." {
			if _, skip := treeSkipDirs[name]; skip {
				continue
			}
			// other dotfiles/dotdirs: skip to keep output focused
			continue
		}
		if _, skip := treeSkipDirs[name]; skip {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, e)
		} else {
			files = append(files, e)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name() < dirs[j].Name() })
	sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })

	for _, d := range dirs {
		if *emitted >= maxEntries {
			*truncated = true
			return nil
		}
		fmt.Fprintf(b, "%s%s/\n", indent, d.Name())
		*emitted++
		sub := filepath.Join(root, d.Name())
		if err := walkTree(sub, indent+"  ", depth+1, maxDepth, maxEntries, emitted, truncated, b); err != nil {
			return err
		}
	}
	for _, f := range files {
		if *emitted >= maxEntries {
			*truncated = true
			return nil
		}
		fmt.Fprintf(b, "%s%s\n", indent, f.Name())
		*emitted++
	}
	return nil
}

func normalizeTreeDepth(d int) int {
	switch {
	case d <= 0:
		return defaultTreeMaxDepth
	case d > maxTreeMaxDepth:
		return maxTreeMaxDepth
	default:
		return d
	}
}

func normalizeTreeMaxEntries(n int) int {
	switch {
	case n <= 0:
		return defaultTreeMaxEntries
	case n > maxTreeMaxEntries:
		return maxTreeMaxEntries
	default:
		return n
	}
}
