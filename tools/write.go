package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

type writeRequest struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
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
	if err := json.Unmarshal([]byte(strings.TrimSpace(input)), &req); err != nil {
		return "", fmt.Errorf("write input must be valid JSON: %w", err)
	}
	req.FilePath = strings.TrimSpace(req.FilePath)
	if req.FilePath == "" {
		return "", fmt.Errorf("file_path is required")
	}

	workspace, target, relativePath, err := resolveFileOperationPath(req.FilePath)
	if err != nil {
		return "", err
	}
	resp, err := writeFileOperation(workspace, target, relativePath, req.Content, false)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
