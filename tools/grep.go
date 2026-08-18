package tools

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/dengzii/weaveflow/llms"
)

const (
	defaultGrepResults  = 250
	maxGrepResults      = 1000
	maxGrepBytesPerLine = 240
	binaryProbeBytes    = 4096
)

type grepRequest struct {
	Pattern         string `json:"pattern"`
	Path            string `json:"path,omitempty"`
	Glob            string `json:"glob,omitempty"`
	Type            string `json:"type,omitempty"`
	OutputMode      string `json:"output_mode,omitempty"`
	Offset          int    `json:"offset,omitempty"`
	Multiline       bool   `json:"multiline,omitempty"`
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
	Pattern    string      `json:"pattern"`
	Root       string      `json:"root"`
	Workspace  string      `json:"workspace"`
	OutputMode string      `json:"output_mode,omitempty"`
	Matches    []grepMatch `json:"matches"`
	Paths      []string    `json:"paths,omitempty"`
	Counts     []grepCount `json:"counts,omitempty"`
	Truncated  bool        `json:"truncated,omitempty"`
	Scanned    int         `json:"scanned_files"`
}

type grepCount struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

func NewGrep() Tool {
	return Tool{
		Function: &llms.FunctionDefinition{
			Name: "grep",
			Description: "A powerful search tool built on ripgrep\n\n" +
				"Usage:\n" +
				"- ALWAYS use Grep for search tasks. NEVER invoke grep or rg as a Bash command.\n" +
				"- Supports regex syntax and filtering files with glob or type parameters.\n" +
				"- Output modes: content shows matching lines, files_with_matches shows only file paths, count shows match counts.",
			OutputSchema: objectOutputSchema(map[string]any{
				"pattern":     map[string]any{"type": "string"},
				"root":        map[string]any{"type": "string"},
				"workspace":   map[string]any{"type": "string"},
				"output_mode": map[string]any{"type": "string"},
				"matches": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"path": map[string]any{"type": "string"},
							"line": map[string]any{"type": "integer"},
							"col":  map[string]any{"type": "integer"},
							"text": map[string]any{"type": "string"},
						},
						"required":             []string{"path", "line", "col", "text"},
						"additionalProperties": false,
					},
				},
				"paths": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"counts": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"path":  map[string]any{"type": "string"},
							"count": map[string]any{"type": "integer"},
						},
						"required":             []string{"path", "count"},
						"additionalProperties": false,
					},
				},
				"truncated":     map[string]any{"type": "boolean"},
				"scanned_files": map[string]any{"type": "integer"},
			}, "pattern", "root", "workspace", "matches", "scanned_files"),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "The regular expression pattern to search for in file contents",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Directory to search. Defaults to workspace root.",
					},
					"glob": map[string]any{
						"type":        "string",
						"description": "Glob pattern to filter files (e.g. \"*.js\", \"*.{ts,tsx}\").",
					},
					"type": map[string]any{
						"type":        "string",
						"description": "File type to search. Common types: js, py, rust, go, java, etc.",
					},
					"output_mode": map[string]any{
						"type":        "string",
						"enum":        []string{"content", "files_with_matches", "count"},
						"description": "Output mode: content, files_with_matches, or count.",
					},
					"max_results": map[string]any{
						"type":        "number",
						"description": "Maximum number of output entries. Defaults to 250 when unspecified.",
					},
					"offset": map[string]any{
						"type":        "number",
						"description": "Skip first N lines/entries before applying max_results. Defaults to 0.",
					},
					"multiline": map[string]any{
						"type":        "boolean",
						"description": "Enable multiline mode.",
					},
					"case_insensitive": map[string]any{
						"type":        "boolean",
						"description": "Enable case-insensitive matching.",
					},
				},
				"required":             []string{"pattern"},
				"additionalProperties": false,
			},
		},
		Handler:     grepTool,
		Effect:      EffectReadOnly,
		Permissions: []string{"filesystem.read"},
	}
}

func grepTool(ctx context.Context, call llms.ToolCall) (llms.ToolResult, error) {
	req, err := parseGrepRequest(call)
	if err != nil {
		return llms.ToolResult{}, err
	}

	pattern := req.Pattern
	if req.CaseInsensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return llms.ToolResult{}, fmt.Errorf("invalid regex: %w", err)
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
		return llms.ToolResult{}, errors.New("grep root must be a directory")
	}

	limit := normalizeGrepLimit(req.MaxResults)
	matches := make([]grepMatch, 0, 32)
	scanned := 0
	truncated := false
	outputMode := normalizeGrepOutputMode(req.OutputMode)

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
		if req.Type != "" && !matchGrepType(req.Type, rel) {
			return nil
		}

		scanned++
		fileMatches, stop := scanFileForGrep(path, re, limit-len(matches))
		matches = append(matches, decorateGrepMatches(fileMatches, joinToolRelativePath(relRoot, rel))...)
		if stop {
			truncated = true
			return filepath.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		return llms.ToolResult{}, walkErr
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Path != matches[j].Path {
			return matches[i].Path < matches[j].Path
		}
		return matches[i].Line < matches[j].Line
	})

	matches = offsetGrepMatches(matches, req.Offset)
	resp := grepResponse{
		Pattern:    req.Pattern,
		Root:       relRoot,
		Workspace:  workspace,
		OutputMode: outputMode,
		Matches:    []grepMatch{},
		Truncated:  truncated,
		Scanned:    scanned,
	}
	switch outputMode {
	case "files_with_matches":
		resp.Paths = grepPathsWithMatches(matches, limit)
	case "count":
		resp.Counts = grepCounts(matches, limit)
	default:
		resp.Matches = limitGrepMatches(matches, limit)
	}

	return structuredToolResult(call, resp)
}

func parseGrepRequest(call llms.ToolCall) (grepRequest, error) {
	var req grepRequest
	if err := decodeToolArguments(call, &req); err != nil {
		return grepRequest{}, err
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

func normalizeGrepOutputMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case "files_with_matches", "count":
		return strings.TrimSpace(mode)
	default:
		return "content"
	}
}

func offsetGrepMatches(matches []grepMatch, offset int) []grepMatch {
	if offset <= 0 {
		return matches
	}
	if offset >= len(matches) {
		return nil
	}
	return matches[offset:]
}

func limitGrepMatches(matches []grepMatch, limit int) []grepMatch {
	if limit <= 0 || len(matches) <= limit {
		return matches
	}
	return matches[:limit]
}

func grepPathsWithMatches(matches []grepMatch, limit int) []string {
	seen := map[string]struct{}{}
	paths := make([]string, 0)
	for _, match := range matches {
		if _, ok := seen[match.Path]; ok {
			continue
		}
		seen[match.Path] = struct{}{}
		paths = append(paths, match.Path)
		if limit > 0 && len(paths) >= limit {
			break
		}
	}
	return paths
}

func grepCounts(matches []grepMatch, limit int) []grepCount {
	countsByPath := map[string]int{}
	for _, match := range matches {
		countsByPath[match.Path]++
	}
	paths := make([]string, 0, len(countsByPath))
	for path := range countsByPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	if limit > 0 && len(paths) > limit {
		paths = paths[:limit]
	}
	counts := make([]grepCount, 0, len(paths))
	for _, path := range paths {
		counts = append(counts, grepCount{Path: path, Count: countsByPath[path]})
	}
	return counts
}

func matchGrepType(fileType string, path string) bool {
	exts, ok := grepTypeExtensions[strings.ToLower(strings.TrimSpace(fileType))]
	if !ok {
		return true
	}
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	for _, candidate := range exts {
		if ext == candidate {
			return true
		}
	}
	return false
}

var grepTypeExtensions = map[string][]string{
	"go":   {"go"},
	"js":   {"js", "jsx", "mjs", "cjs"},
	"ts":   {"ts", "tsx", "mts", "cts"},
	"py":   {"py"},
	"rust": {"rs"},
	"java": {"java"},
	"json": {"json"},
	"md":   {"md", "markdown"},
	"yaml": {"yaml", "yml"},
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
