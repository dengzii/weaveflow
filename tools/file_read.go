package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

func NewFileRead() Tool {
	return Tool{
		Function: &llms.FunctionDefinition{
			Name: "file_read",
			Description: "Read-only file operations inside the workspace. " +
				"Supported actions: read, list, stat, exists. " +
				"Paths must stay inside the workspace.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"enum":        []string{"read", "list", "stat", "exists"},
						"description": "The file operation to perform.",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "A relative path inside the workspace, such as docs/report.md or .",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Optional max bytes for read or max entries for list.",
					},
				},
				"required":             []string{"action", "path"},
				"additionalProperties": false,
			},
		},
		Handler: fileReadTool,
	}
}

func fileReadTool(_ context.Context, input string) (string, error) {
	req, err := parseFileOperationRequest(input)
	if err != nil {
		return "", err
	}

	workspace, target, relativePath, err := resolveFileOperationPath(req.Path)
	if err != nil {
		return "", err
	}

	var resp fileOperationResponse
	switch req.Action {
	case "read":
		resp, err = readFileOperation(workspace, target, relativePath, req.Limit)
	case "list":
		resp, err = listFileOperation(workspace, target, relativePath, req.Limit)
	case "stat":
		resp, err = statFileOperation(workspace, target, relativePath)
	case "exists":
		resp, err = existsFileOperation(workspace, target, relativePath)
	default:
		err = fmt.Errorf("unsupported file_read action %q; use file_write for write/append/mkdir", req.Action)
	}
	if err != nil {
		return "", err
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func parseFileOperationRequest(input string) (fileOperationRequest, error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return fileOperationRequest{}, fmt.Errorf("file operation input is required")
	}

	var req fileOperationRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		return fileOperationRequest{}, fmt.Errorf("file operation input must be valid JSON: %w", err)
	}

	req.Action = strings.TrimSpace(strings.ToLower(req.Action))
	req.Path = strings.TrimSpace(req.Path)
	if req.Action == "" {
		return fileOperationRequest{}, fmt.Errorf("file operation action is required")
	}
	if req.Path == "" {
		return fileOperationRequest{}, fmt.Errorf("file operation path is required")
	}
	return req, nil
}
