package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/tmc/langchaingo/llms"
)

const defaultReadLines = 2000

type readRequest struct {
	FilePath string `json:"file_path"`
	Offset   int    `json:"offset,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	Pages    string `json:"pages,omitempty"`
}

func NewRead() Tool {
	return Tool{
		Function: &llms.FunctionDefinition{
			Name: "read",
			Description: "Reads a file from the local filesystem.\n" +
				"Usage:\n" +
				"- The file_path parameter should be an absolute path.\n" +
				"- By default, it reads up to 2000 lines from the beginning of the file.\n" +
				"- You can optionally specify a line offset and limit for long files.\n" +
				"- Results are returned using cat -n format, with line numbers starting at 1.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "The absolute path to the file to read",
					},
					"offset": map[string]any{
						"type":        "integer",
						"minimum":     0,
						"description": "The line number to start reading from. Only provide if the file is too large to read at once",
					},
					"limit": map[string]any{
						"type":             "integer",
						"exclusiveMinimum": 0,
						"description":      "The number of lines to read. Only provide if the file is too large to read at once.",
					},
					"pages": map[string]any{
						"type":        "string",
						"description": "Page range for PDF files. Accepted for schema compatibility; PDF parsing is not implemented.",
					},
				},
				"required":             []string{"file_path"},
				"additionalProperties": false,
			},
		},
		Handler: readTool,
	}
}

func readTool(_ context.Context, input string) (string, error) {
	var req readRequest
	if err := json.Unmarshal([]byte(strings.TrimSpace(input)), &req); err != nil {
		return "", fmt.Errorf("read input must be valid JSON: %w", err)
	}
	req.FilePath = strings.TrimSpace(req.FilePath)
	if req.FilePath == "" {
		return "", fmt.Errorf("file_path is required")
	}

	_, target, relativePath, err := resolveToolPath(req.FilePath)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("path is a directory")
	}

	data, err := os.ReadFile(target)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("file is not a UTF-8 text file")
	}

	return formatReadLines(relativePath, string(data), req.Offset, req.Limit), nil
}

func formatReadLines(path string, content string, offset int, limit int) string {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = defaultReadLines
	}

	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if offset >= len(lines) {
		return fmt.Sprintf("%s\n", path)
	}
	end := offset + limit
	if end > len(lines) {
		end = len(lines)
	}

	var b strings.Builder
	b.WriteString(path)
	b.WriteByte('\n')
	for i := offset; i < end; i++ {
		_, _ = fmt.Fprintf(&b, "%d\t%s\n", i+1, lines[i])
	}
	if end < len(lines) {
		_, _ = fmt.Fprintf(&b, "[truncated: showing lines %d-%d of %d]\n", offset+1, end, len(lines))
	}
	return b.String()
}
