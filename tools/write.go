package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/llms"
)

type writeRequest struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

type writeResponse struct {
	Action       string `json:"action"`
	Path         string `json:"path"`
	Workspace    string `json:"workspace"`
	Exists       bool   `json:"exists"`
	Size         int64  `json:"size"`
	BytesWritten int    `json:"bytes_written"`
}

func NewWrite() Tool {
	return Tool{
		Function: &llms.FunctionDefinition{
			Name: "write",
			Description: "Writes a file to the local filesystem.\n\n" +
				"Usage:\n" +
				"- This tool overwrites the existing file if there is one at the provided path.\n" +
				"- Prefer Edit for modifying existing files when replacing text is enough.",
			OutputSchema: objectOutputSchema(map[string]any{
				"action":        map[string]any{"type": "string"},
				"path":          map[string]any{"type": "string"},
				"workspace":     map[string]any{"type": "string"},
				"exists":        map[string]any{"type": "boolean"},
				"size":          map[string]any{"type": "integer"},
				"bytes_written": map[string]any{"type": "integer"},
			}, "action", "path", "workspace", "exists", "size", "bytes_written"),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "File to write.",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "The content to write to the file",
					},
				},
				"required":             []string{"file_path", "content"},
				"additionalProperties": false,
			},
		},
		Handler:     writeTool,
		Effect:      EffectIdempotentWrite,
		Permissions: []string{"filesystem.write"},
		Approval:    core.ToolApprovalRequired,
	}
}

func writeTool(ctx context.Context, call llms.ToolCall) (llms.ToolResult, error) {
	var req writeRequest
	if err := decodeToolArguments(call, &req); err != nil {
		return llms.ToolResult{}, fmt.Errorf("write input: %w", err)
	}
	req.FilePath = strings.TrimSpace(req.FilePath)
	if req.FilePath == "" {
		return llms.ToolResult{}, fmt.Errorf("file_path is required")
	}

	workspace, target, relativePath, err := resolveToolPath(ctx, req.FilePath)
	if err != nil {
		return llms.ToolResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return llms.ToolResult{}, err
	}
	if err := os.WriteFile(target, []byte(req.Content), 0o644); err != nil {
		return llms.ToolResult{}, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return llms.ToolResult{}, err
	}
	resp := writeResponse{
		Action:       "write",
		Path:         relativePath,
		Workspace:    workspace,
		Exists:       true,
		Size:         info.Size(),
		BytesWritten: len(req.Content),
	}
	return structuredToolResult(call, resp)
}
