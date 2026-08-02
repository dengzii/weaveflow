package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

type writeRequest struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

type writeResponse struct {
	Action       string `json:"action"`
	Path         string `json:"path"`
	Workspace    string `json:"workspace"`
	Exists       bool   `json:"exists,omitempty"`
	Size         int64  `json:"size,omitempty"`
	BytesWritten int    `json:"bytes_written,omitempty"`
}

func NewWrite() Tool {
	return Tool{
		Function: &llms.FunctionDefinition{
			Name: "write",
			Description: "Writes a file to the local filesystem.\n\n" +
				"Usage:\n" +
				"- This tool overwrites the existing file if there is one at the provided path.\n" +
				"- Prefer Edit for modifying existing files when replacing text is enough.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "The absolute path to the file to write (must be absolute, not relative)",
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
		Handler: writeTool,
	}
}

func writeTool(_ context.Context, input string) (string, error) {
	var req writeRequest
	if err := decodeToolRequest(input, "write", &req); err != nil {
		return "", err
	}
	req.FilePath = strings.TrimSpace(req.FilePath)
	if req.FilePath == "" {
		return "", fmt.Errorf("file_path is required")
	}

	workspace, target, relativePath, err := resolveToolPath(req.FilePath)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(target, []byte(req.Content), 0o644); err != nil {
		return "", err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", err
	}
	resp := writeResponse{
		Action:       "write",
		Path:         relativePath,
		Workspace:    workspace,
		Exists:       true,
		Size:         info.Size(),
		BytesWritten: len(req.Content),
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
