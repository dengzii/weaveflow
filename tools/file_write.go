package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tmc/langchaingo/llms"
)

func NewFileWrite() Tool {
	return Tool{
		Function: &llms.FunctionDefinition{
			Name: "file_write",
			Description: "Write file operations inside the workspace. " +
				"Supported actions: write, append, mkdir. " +
				"Paths must stay inside the workspace.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"enum":        []string{"write", "append", "mkdir"},
						"description": "The file operation to perform.",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "A relative path inside the workspace, such as docs/report.md or .",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "Content used by write and append.",
					},
				},
				"required":             []string{"action", "path"},
				"additionalProperties": false,
			},
		},
		Handler: fileWriteTool,
	}
}

func fileWriteTool(_ context.Context, input string) (string, error) {
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
	case "write":
		resp, err = writeFileOperation(workspace, target, relativePath, req.Content, false)
	case "append":
		resp, err = writeFileOperation(workspace, target, relativePath, req.Content, true)
	case "mkdir":
		resp, err = mkdirFileOperation(workspace, target, relativePath)
	default:
		err = fmt.Errorf("unsupported file_write action %q; use file_read for read/list/stat/exists", req.Action)
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
