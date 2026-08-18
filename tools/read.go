package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/dengzii/weaveflow/llms"
)

const defaultReadLines = 2000

type readRequest struct {
	FilePath string `json:"file_path"`
	Offset   int    `json:"offset,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

func NewRead() Tool {
	return Tool{
		Function: &llms.FunctionDefinition{
			Name: "read",
			Description: "Reads a file from the local filesystem.\n" +
				"Usage:\n" +
				"- By default, it reads up to 2000 lines from the beginning of the file.\n" +
				"- You can optionally specify a line offset and limit for long files.\n" +
				"- Results are returned using cat -n format, with line numbers starting at 1.",
			OutputSchema: textOutputSchema(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "File to read.",
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
				},
				"required":             []string{"file_path"},
				"additionalProperties": false,
			},
		},
		Handler:     readTool,
		Effect:      EffectReadOnly,
		Permissions: []string{"filesystem.read"},
	}
}

func readTool(ctx context.Context, call llms.ToolCall) (llms.ToolResult, error) {
	var req readRequest
	if err := decodeToolArguments(call, &req); err != nil {
		return llms.ToolResult{}, fmt.Errorf("read input: %w", err)
	}
	req.FilePath = strings.TrimSpace(req.FilePath)
	if req.FilePath == "" {
		return llms.ToolResult{}, fmt.Errorf("file_path is required")
	}

	_, target, relativePath, err := resolveToolPath(ctx, req.FilePath)
	if err != nil {
		return llms.ToolResult{}, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return llms.ToolResult{}, err
	}
	if info.IsDir() {
		return llms.ToolResult{}, fmt.Errorf("path is a directory")
	}

	data, err := os.ReadFile(target)
	if err != nil {
		return llms.ToolResult{}, err
	}
	if !utf8.Valid(data) {
		return llms.ToolResult{}, fmt.Errorf("file is not a UTF-8 text file")
	}

	return textToolResult(call, formatReadLines(relativePath, string(data), req.Offset, req.Limit)), nil
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
