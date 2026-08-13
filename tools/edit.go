package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/llms"
)

type editRequest struct {
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

type editResponse struct {
	Action       string `json:"action"`
	Path         string `json:"path"`
	Workspace    string `json:"workspace"`
	Replacements int    `json:"replacements"`
	Size         int64  `json:"size"`
}

func NewEdit() Tool {
	return Tool{
		Function: &llms.FunctionDefinition{
			Name: "edit",
			Description: "Performs exact string replacements in files.\n\n" +
				"Usage:\n" +
				"- The edit fails if old_string is not unique and replace_all is false.\n" +
				"- Use replace_all to change every instance of old_string.",
			OutputSchema: objectOutputSchema(map[string]any{
				"action":       map[string]any{"type": "string"},
				"path":         map[string]any{"type": "string"},
				"workspace":    map[string]any{"type": "string"},
				"replacements": map[string]any{"type": "integer"},
				"size":         map[string]any{"type": "integer"},
			}, "action", "path", "workspace", "replacements", "size"),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "File to modify.",
					},
					"old_string": map[string]any{
						"type":        "string",
						"description": "The text to replace",
					},
					"new_string": map[string]any{
						"type":        "string",
						"description": "The text to replace it with (must be different from old_string)",
					},
					"replace_all": map[string]any{
						"type":        "boolean",
						"default":     false,
						"description": "Replace all occurrences of old_string (default false)",
					},
				},
				"required":             []string{"file_path", "old_string", "new_string"},
				"additionalProperties": false,
			},
		},
		Handler:     editTool,
		Permissions: []string{"filesystem.write"},
		Approval:    core.ToolApprovalRequired,
	}
}

func editTool(ctx context.Context, call llms.ToolCall) (llms.ToolResult, error) {
	var req editRequest
	if err := decodeToolArguments(call, &req); err != nil {
		return llms.ToolResult{}, fmt.Errorf("edit input: %w", err)
	}
	req.FilePath = strings.TrimSpace(req.FilePath)
	if req.FilePath == "" {
		return llms.ToolResult{}, fmt.Errorf("file_path is required")
	}
	if req.OldString == "" {
		return llms.ToolResult{}, fmt.Errorf("old_string is required")
	}
	if req.OldString == req.NewString {
		return llms.ToolResult{}, fmt.Errorf("new_string must be different from old_string")
	}

	workspace, target, relativePath, err := resolveToolPath(ctx, req.FilePath)
	if err != nil {
		return llms.ToolResult{}, err
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return llms.ToolResult{}, err
	}
	if !utf8.Valid(data) {
		return llms.ToolResult{}, fmt.Errorf("file is not a UTF-8 text file")
	}

	content := string(data)
	count := strings.Count(content, req.OldString)
	if count == 0 {
		return llms.ToolResult{}, fmt.Errorf("old_string not found")
	}
	if count > 1 && !req.ReplaceAll {
		return llms.ToolResult{}, fmt.Errorf("old_string is not unique; found %d occurrences", count)
	}

	replaceCount := 1
	if req.ReplaceAll {
		replaceCount = -1
	}
	updated := strings.Replace(content, req.OldString, req.NewString, replaceCount)
	if err := os.WriteFile(target, []byte(updated), 0o644); err != nil {
		return llms.ToolResult{}, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return llms.ToolResult{}, err
	}
	resp := editResponse{
		Action:       "edit",
		Path:         relativePath,
		Workspace:    workspace,
		Replacements: count,
		Size:         info.Size(),
	}
	if !req.ReplaceAll {
		resp.Replacements = 1
	}
	return structuredToolResult(call, resp)
}
