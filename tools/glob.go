package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tmc/langchaingo/llms"
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
	Pattern   string   `json:"pattern"`
	Root      string   `json:"root"`
	Workspace string   `json:"workspace"`
	Paths     []string `json:"paths"`
	Truncated bool     `json:"truncated,omitempty"`
	Scanned   int      `json:"scanned_files"`
}

func NewGlob() Tool {
	return Tool{
		Function: &llms.FunctionDefinition{
			Name: "glob",
			Description: "Find filenames matching a glob pattern inside the workspace. " +
				"Supports ** for recursive descent and * for any-name within a path segment. " +
				"Prefer this over listing directories one by one.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "Glob pattern such as **/*.go or internal/**/*_test.go",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Optional root directory inside the workspace; defaults to .",
					},
					"max_results": map[string]any{
						"type":        "integer",
						"description": "Optional cap on returned paths (default 200, max 500).",
					},
				},
				"required":             []string{"pattern"},
				"additionalProperties": false,
			},
		},
		Handler: globTool,
	}
}

func globTool(_ context.Context, input string) (string, error) {
	req, err := parseGlobRequest(input)
	if err != nil {
		return "", err
	}

	root := req.Path
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	workspace, target, relRoot, err := resolveFileOperationPath(root)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("glob root must be a directory")
	}

	limit := normalizeGlobLimit(req.MaxResults)
	matched := make([]string, 0, 32)
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
		matched = append(matched, joinRelativePath(relRoot, rel))
		return nil
	})
	if walkErr != nil {
		return "", walkErr
	}

	sort.Strings(matched)

	data, err := json.Marshal(globResponse{
		Pattern:   req.Pattern,
		Root:      relRoot,
		Workspace: workspace,
		Paths:     matched,
		Truncated: truncated,
		Scanned:   scanned,
	})
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func parseGlobRequest(input string) (globRequest, error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return globRequest{}, fmt.Errorf("glob input is required")
	}
	var req globRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		return globRequest{}, fmt.Errorf("glob input must be valid JSON: %w", err)
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
