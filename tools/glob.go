package tools

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dengzii/weaveflow/llms"
)

const (
	defaultGlobResults = 200
	maxGlobResults     = 500
)

var globSkipDirs = map[string]struct{}{
	".git":         {},
	"node_modules": {},
	"vendor":       {},
	"dist":         {},
	".idea":        {},
	".vscode":      {},
}

type globRequest struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path,omitempty"`
	MaxResults int    `json:"max_results,omitempty"`
}

type globResponse struct {
	Pattern   string      `json:"pattern"`
	Root      string      `json:"root"`
	Workspace string      `json:"workspace"`
	Paths     []globMatch `json:"paths"`
	Truncated bool        `json:"truncated,omitempty"`
	Scanned   int         `json:"scanned_files"`
}

type globMatch struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type globResult struct {
	Path    string
	Size    int64
	ModTime int64
}

func NewGlob() Tool {
	return Tool{
		Function: &llms.FunctionDefinition{
			Name: "glob",
			Description: "- Fast file pattern matching tool that works with any codebase size\n" +
				"- Supports glob patterns like \"**/*.js\" or \"src/**/*.ts\"\n" +
				"- Returns matching file paths sorted by modification time\n" +
				"- Use this tool when you need to find files by name patterns",
			OutputSchema: objectOutputSchema(map[string]any{
				"pattern":   map[string]any{"type": "string"},
				"root":      map[string]any{"type": "string"},
				"workspace": map[string]any{"type": "string"},
				"paths": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"path": map[string]any{"type": "string"},
							"size": map[string]any{"type": "integer"},
						},
						"required":             []string{"path", "size"},
						"additionalProperties": false,
					},
				},
				"truncated":     map[string]any{"type": "boolean"},
				"scanned_files": map[string]any{"type": "integer"},
			}, "pattern", "root", "workspace", "paths", "scanned_files"),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "The glob pattern to match files against",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Directory to search. Defaults to workspace root.",
					},
					"max_results": map[string]any{
						"type":             "integer",
						"exclusiveMinimum": 0,
						"description":      "Maximum number of matching paths. Defaults to 200, capped at 500.",
					},
				},
				"required":             []string{"pattern"},
				"additionalProperties": false,
			},
		},
		Handler:     globTool,
		Permissions: []string{"filesystem.read"},
	}
}

func globTool(ctx context.Context, call llms.ToolCall) (llms.ToolResult, error) {
	req, err := parseGlobRequest(call)
	if err != nil {
		return llms.ToolResult{}, err
	}

	root := req.Path
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	workspace, target, relRoot, err := resolveToolPath(ctx, root)
	if err != nil {
		return llms.ToolResult{}, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return llms.ToolResult{}, err
	}
	if !info.IsDir() {
		return llms.ToolResult{}, errors.New("glob root must be a directory")
	}

	limit := normalizeGlobLimit(req.MaxResults)
	matched := make([]globResult, 0, 32)
	scanned := 0
	truncated := false

	walkErr := filepath.WalkDir(target, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrPermission) {
				return nil
			}
			return walkErr
		}
		if d.IsDir() {
			name := d.Name()
			if _, skip := globSkipDirs[name]; skip && path != target {
				return fs.SkipDir
			}
			return nil
		}

		scanned++
		rel, err := filepath.Rel(target, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if !matchGlob(req.Pattern, rel) {
			return nil
		}

		if len(matched) >= limit {
			truncated = true
			return filepath.SkipAll
		}
		var (
			modTime int64
			size    int64
		)
		if info, err := d.Info(); err == nil {
			modTime = info.ModTime().UnixNano()
			size = info.Size()
		}
		matched = append(matched, globResult{
			Path:    joinToolRelativePath(relRoot, rel),
			Size:    size,
			ModTime: modTime,
		})
		return nil
	})
	if walkErr != nil {
		return llms.ToolResult{}, walkErr
	}

	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].ModTime != matched[j].ModTime {
			return matched[i].ModTime > matched[j].ModTime
		}
		return matched[i].Path < matched[j].Path
	})
	paths := make([]globMatch, len(matched))
	for i, item := range matched {
		paths[i] = globMatch{Path: item.Path, Size: item.Size}
	}

	response := globResponse{
		Pattern:   req.Pattern,
		Root:      relRoot,
		Workspace: workspace,
		Paths:     paths,
		Truncated: truncated,
		Scanned:   scanned,
	}
	return structuredToolResult(call, response)
}

func parseGlobRequest(call llms.ToolCall) (globRequest, error) {
	var req globRequest
	if err := decodeToolArguments(call, &req); err != nil {
		return globRequest{}, err
	}
	req.Pattern = strings.TrimSpace(req.Pattern)
	if req.Pattern == "" {
		return globRequest{}, fmt.Errorf("glob pattern is required")
	}
	return req, nil
}

func normalizeGlobLimit(limit int) int {
	if limit <= 0 {
		return defaultGlobResults
	}
	if limit > maxGlobResults {
		return maxGlobResults
	}
	return limit
}

// matchGlob matches a slash-delimited path against a glob pattern that supports
// `**` (any depth of segments, including zero), `*` (any name within a single
// segment), `?` (single char), and `[...]` character classes. The path is
// expected to use `/` separators (callers convert via filepath.ToSlash).
func matchGlob(pattern, path string) bool {
	return globSegmentMatch(splitGlobSegments(pattern), splitGlobSegments(path))
}

func splitGlobSegments(s string) []string {
	s = strings.Trim(s, "/")
	if s == "" {
		return nil
	}
	return strings.Split(s, "/")
}

func globSegmentMatch(patSeg, pathSeg []string) bool {
	for len(patSeg) > 0 {
		p := patSeg[0]
		if p == "**" {
			rest := patSeg[1:]
			if len(rest) == 0 {
				return true
			}
			for i := 0; i <= len(pathSeg); i++ {
				if globSegmentMatch(rest, pathSeg[i:]) {
					return true
				}
			}
			return false
		}
		if len(pathSeg) == 0 {
			return false
		}
		ok, err := filepath.Match(p, pathSeg[0])
		if err != nil || !ok {
			return false
		}
		patSeg = patSeg[1:]
		pathSeg = pathSeg[1:]
	}
	return len(pathSeg) == 0
}
