package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/llms"
)

func WithReadLimits(tool core.Tool, maxLines, maxOutputBytes int) core.Tool {
	if tool.Handler == nil || (maxLines <= 0 && maxOutputBytes <= 0) {
		return tool
	}
	if tool.Function != nil {
		definition := *tool.Function
		definition.Parameters = definition.Parameters.Clone()
		if maxLines > 0 {
			definition.Description = strings.TrimSpace(definition.Description) + fmt.Sprintf("\nRead limit: at most %d lines per call.", maxLines)
			if properties, ok := definition.Parameters["properties"].(map[string]any); ok {
				if limitSchema, ok := properties["limit"].(map[string]any); ok {
					limitSchema["maximum"] = maxLines
				}
			}
		}
		tool.Function = &definition
	}
	handler := tool.Handler
	tool.Handler = func(ctx context.Context, call llms.ToolCall) (llms.ToolResult, error) {
		if call.FunctionCall == nil {
			return llms.ToolResult{}, errors.New("read call has no function payload")
		}
		if maxLines > 0 {
			var arguments map[string]any
			if err := json.Unmarshal(call.FunctionCall.Arguments, &arguments); err != nil {
				return llms.ToolResult{}, fmt.Errorf("decode bounded read arguments: %w", err)
			}
			limit := intArgument(arguments["limit"])
			if limit <= 0 {
				arguments["limit"] = maxLines
			} else if limit > maxLines {
				return llms.ToolResult{}, fmt.Errorf("read limit %d exceeds maximum %d", limit, maxLines)
			}
			encoded, err := json.Marshal(arguments)
			if err != nil {
				return llms.ToolResult{}, fmt.Errorf("encode bounded read arguments: %w", err)
			}
			functionCall := *call.FunctionCall
			functionCall.Arguments = encoded
			call.FunctionCall = &functionCall
		}
		result, err := handler(ctx, call)
		if maxOutputBytes > 0 {
			result.Content = limitToolOutput(result.Content, maxOutputBytes)
			if _, ok := result.Value.(string); ok {
				result.Value = result.Content
			}
		}
		return result, err
	}
	return tool
}

func WithPathScope(tool core.Tool, configured []string) core.Tool {
	if tool.Function == nil {
		return tool
	}
	scope := "workspace root"
	if len(configured) > 0 {
		scope = strings.Join(configured, ", ")
	}
	definition := *tool.Function
	definition.Description = strings.TrimSpace(definition.Description) + "\nPath scope: " + scope + ". Use paths relative to the workspace."
	tool.Function = &definition
	return tool
}

func GuardWorkspaceTool(tool core.Tool, workspace string, allowedPaths []string) core.Tool {
	if tool.Handler == nil {
		return tool
	}
	handler := tool.Handler
	tool.Handler = func(ctx context.Context, call llms.ToolCall) (llms.ToolResult, error) {
		if err := ValidateToolPaths(workspace, allowedPaths, call); err != nil {
			return llms.ToolResult{}, err
		}
		environment := core.EnvironmentFromContext(ctx)
		if environment == nil {
			environment = make(map[string]string)
		}
		environment[toolWorkspaceEnv] = workspace
		return handler(core.WithEnvironment(ctx, environment), call)
	}
	return tool
}

func ResolveAllowedPaths(workspace string, configured []string) ([]string, error) {
	root, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	if len(configured) == 0 {
		return []string{root}, nil
	}
	allowed := make([]string, 0, len(configured))
	for _, value := range configured {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, errors.New("allowed path is empty")
		}
		target, err := SecureWorkspacePath(root, value, true)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", value, err)
		}
		resolved, err := filepath.EvalSymlinks(target)
		if err != nil {
			return nil, fmt.Errorf("%q: resolve: %w", value, err)
		}
		allowed = append(allowed, resolved)
	}
	return allowed, nil
}

func ValidateToolPaths(workspace string, allowedPaths []string, call llms.ToolCall) error {
	if call.FunctionCall == nil {
		return errors.New("tool call has no function payload")
	}
	var arguments map[string]any
	if err := json.Unmarshal(call.FunctionCall.Arguments, &arguments); err != nil {
		return fmt.Errorf("decode tool paths: %w", err)
	}
	for _, key := range []string{"file_path", "path"} {
		value, _ := arguments[key].(string)
		if strings.TrimSpace(value) == "" && (call.FunctionCall.Name == "grep" || call.FunctionCall.Name == "glob") && key == "path" {
			value = "."
		}
		if strings.TrimSpace(value) == "" {
			continue
		}
		mustExist := call.FunctionCall.Name != "write"
		target, err := SecureWorkspacePath(workspace, value, mustExist)
		if err != nil {
			return err
		}
		resolved, resolveErr := filepath.EvalSymlinks(target)
		if resolveErr != nil {
			resolved, resolveErr = filepath.Abs(target)
		}
		if resolveErr != nil || !PathAllowed(resolved, allowedPaths) {
			return fmt.Errorf("path %q is outside the configured tool path scope", value)
		}
	}
	return nil
}

func PathAllowed(target string, allowedPaths []string) bool {
	for _, allowed := range allowedPaths {
		if target == allowed || pathWithin(allowed, target) {
			return true
		}
	}
	return false
}

func SecureWorkspacePath(workspace, value string, mustExist bool) (string, error) {
	root, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	target := filepath.Clean(value)
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if !pathWithin(root, target) {
		return "", errors.New("path escapes workspace")
	}
	resolved := target
	if mustExist {
		resolved, err = filepath.EvalSymlinks(target)
		if err != nil {
			return "", err
		}
	} else {
		if _, statErr := os.Lstat(target); statErr == nil {
			resolved, err = filepath.EvalSymlinks(target)
			if err != nil {
				return "", err
			}
			if !pathWithin(root, resolved) {
				return "", errors.New("path resolves outside workspace")
			}
			return target, nil
		}
		ancestor := filepath.Dir(target)
		for {
			if _, statErr := os.Lstat(ancestor); statErr == nil {
				break
			}
			parent := filepath.Dir(ancestor)
			if parent == ancestor {
				return "", errors.New("cannot resolve workspace path")
			}
			ancestor = parent
		}
		resolvedAncestor, resolveErr := filepath.EvalSymlinks(ancestor)
		if resolveErr != nil {
			return "", resolveErr
		}
		resolved = filepath.Join(resolvedAncestor, strings.TrimPrefix(target, ancestor+string(os.PathSeparator)))
	}
	if !pathWithin(root, resolved) {
		return "", errors.New("path resolves outside workspace")
	}
	return target, nil
}

func WorkspaceFromContext(ctx context.Context) (string, error) {
	return toolWorkspaceDir(ctx)
}

func pathWithin(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func intArgument(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	default:
		return 0
	}
}

func limitToolOutput(value string, maximum int) string {
	if maximum <= 0 || len(value) <= maximum {
		return value
	}
	prefix := strings.ToValidUTF8(value[:maximum], "")
	return strings.TrimSpace(prefix) + "\n[truncated by tool output limit]"
}
