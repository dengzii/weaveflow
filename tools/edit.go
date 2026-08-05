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
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "The absolute path to the file to modify",
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
		Handler: editTool,
	}
}

func editTool(ctx context.Context, input string) (string, error) {
	var req editRequest
	if err := decodeToolRequest(input, "edit", &req); err != nil {
		return "", err
	}
	req.FilePath = strings.TrimSpace(req.FilePath)
	if req.FilePath == "" {
		return "", fmt.Errorf("file_path is required")
	}
	if req.OldString == "" {
		return "", fmt.Errorf("old_string is required")
	}
	if req.OldString == req.NewString {
		return "", fmt.Errorf("new_string must be different from old_string")
	}

	workspace, target, relativePath, err := resolveToolPath(ctx, req.FilePath)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("file is not a UTF-8 text file")
	}

	content := string(data)
	count := strings.Count(content, req.OldString)
	if count == 0 {
		return "", fmt.Errorf("old_string not found")
	}
	if count > 1 && !req.ReplaceAll {
		return "", fmt.Errorf("old_string is not unique; found %d occurrences", count)
	}

	replaceCount := 1
	if req.ReplaceAll {
		replaceCount = -1
	}
	updated := strings.Replace(content, req.OldString, req.NewString, replaceCount)
	if err := os.WriteFile(target, []byte(updated), 0o644); err != nil {
		return "", err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", err
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
	data, err = json.Marshal(resp)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
