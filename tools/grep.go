package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/tmc/langchaingo/llms"
)

const (
	defaultGrepResults  = 80
	maxGrepResults      = 200
	maxGrepBytesPerLine = 240
	binaryProbeBytes    = 4096
)

type grepRequest struct {
	Pattern         string `json:"pattern"`
	Path            string `json:"path,omitempty"`
	Glob            string `json:"glob,omitempty"`
	MaxResults      int    `json:"max_results,omitempty"`
	CaseInsensitive bool   `json:"case_insensitive,omitempty"`
}

type grepMatch struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
	Text string `json:"text"`
}

type grepResponse struct {
	Pattern   string      `json:"pattern"`
	Root      string      `json:"root"`
	Workspace string      `json:"workspace"`
	Matches   []grepMatch `json:"matches"`
	Truncated bool        `json:"truncated,omitempty"`
	Scanned   int         `json:"scanned_files"`
}

func NewGrep() Tool {
	return Tool{
		Function: &llms.FunctionDefinition{
			Name: "grep",
			Description: "Search file contents inside the workspace by regular expression. " +
				"Returns file:line matches with a short snippet. " +
				"Prefer this over reading whole files when looking for specific symbols, strings, or patterns.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "Regular expression (Go RE2 syntax).",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Optional root directory inside the workspace; defaults to .",
					},
					"glob": map[string]any{
						"type":        "string",
						"description": "Optional filename glob filter such as **/*.go.",
					},
					"max_results": map[string]any{
						"type":        "integer",
						"description": "Optional cap on returned matches (default 80, max 200).",
					},
					"case_insensitive": map[string]any{
						"type":        "boolean",
						"description": "Match case-insensitively.",
					},
				},
				"required":             []string{"pattern"},
				"additionalProperties": false,
			},
		},
		Handler: grepTool,
	}
}

func grepTool(_ context.Context, input string) (string, error) {
	req, err := parseGrepRequest(input)
	if err != nil {
		return "", err
	}

	pattern := req.Pattern
	if req.CaseInsensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regex: %w", err)
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
		return "", errors.New("grep root must be a directory")
	}

	limit := normalizeGrepLimit(req.MaxResults)
	matches := make([]grepMatch, 0, 32)
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

		rel, err := filepath.Rel(target, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if req.Glob != "" && !matchGlob(req.Glob, rel) {
			return nil
		}

		scanned++
		fileMatches, stop := scanFileForGrep(path, re, limit-len(matches))
		matches = append(matches, decorateGrepMatches(fileMatches, joinRelativePath(relRoot, rel))...)
		if stop {
			truncated = true
			return filepath.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		return "", walkErr
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Path != matches[j].Path {
			return matches[i].Path < matches[j].Path
		}
		return matches[i].Line < matches[j].Line
	})

	data, err := json.Marshal(grepResponse{
		Pattern:   req.Pattern,
		Root:      relRoot,
		Workspace: workspace,
		Matches:   matches,
		Truncated: truncated,
		Scanned:   scanned,
	})
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func parseGrepRequest(input string) (grepRequest, error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return grepRequest{}, fmt.Errorf("grep input is required")
	}
	var req grepRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		return grepRequest{}, fmt.Errorf("grep input must be valid JSON: %w", err)
	}
	req.Pattern = strings.TrimSpace(req.Pattern)
	if req.Pattern == "" {
		return grepRequest{}, fmt.Errorf("grep pattern is required")
	}
	return req, nil
}

func normalizeGrepLimit(limit int) int {
	if limit <= 0 {
		return defaultGrepResults
	}
	if limit > maxGrepResults {
		return maxGrepResults
	}
	return limit
}

// scanFileForGrep reads a single file and collects regex matches up to budget.
// stop is true when budget is exhausted; the caller should stop walking.
func scanFileForGrep(path string, re *regexp.Regexp, budget int) (matches []grepMatch, stop bool) {
	if budget <= 0 {
		return nil, true
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer func() {
		_ = f.Close()
	}()

	// Binary sniff: read first chunk; skip files that contain NUL bytes
	// or are not valid UTF-8.
	head := make([]byte, binaryProbeBytes)
	n, _ := f.Read(head)
	head = head[:n]
	if !utf8.Valid(head) {
		return nil, false
	}
	for _, b := range head {
		if b == 0 {
			return nil, false
		}
	}
	if _, err := f.Seek(0, 0); err != nil {
		return nil, false
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		loc := re.FindStringIndex(line)
		if loc == nil {
			continue
		}
		matches = append(matches, grepMatch{
			Line: lineNo,
			Col:  loc[0] + 1,
			Text: truncateGrepLine(line),
		})
		if len(matches) >= budget {
			return matches, true
		}
	}
	return matches, false
}

func decorateGrepMatches(matches []grepMatch, path string) []grepMatch {
	for i := range matches {
		matches[i].Path = path
	}
	return matches
}

func truncateGrepLine(s string) string {
	if len(s) <= maxGrepBytesPerLine {
		return s
	}
	// Trim to maxGrepBytesPerLine bytes but respect UTF-8 boundary.
	cut := maxGrepBytesPerLine
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
