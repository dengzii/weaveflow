package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/dengzii/weaveflow/core"
)

const (
	toolWorkspaceEnv          = "WEAVEFLOW_TOOL_WORKDIR"
	toolSkipWorkspaceCheckEnv = "WEAVEFLOW_TOOL_SKIP_WORKSPACE_CHECK"
)

func toolWorkspaceDir(ctx context.Context) (string, error) {
	dir := toolEnvironmentVariable(ctx, toolWorkspaceEnv)
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	return filepath.Abs(dir)
}

func resolveToolPath(ctx context.Context, path string) (workspace string, target string, relative string, err error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", "", "", errors.New("tool path is required")
	}

	workspace, err = toolWorkspaceDir(ctx)
	if err != nil {
		return "", "", "", err
	}

	if !filepath.IsAbs(path) {
		path, err = normalizeToolPathForOS(path, runtime.GOOS)
		if err != nil {
			return "", "", "", err
		}
	}

	cleanPath := filepath.Clean(path)
	if filepath.IsAbs(cleanPath) {
		target = cleanPath
	} else {
		target = filepath.Join(workspace, cleanPath)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return "", "", "", err
	}

	relative, err = filepath.Rel(workspace, target)
	if err != nil {
		return "", "", "", err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		if !skipToolWorkspaceCheckEnabled(ctx) {
			return "", "", "", errors.New("path escapes workspace")
		}
		relative = filepath.ToSlash(target)
	}

	return filepath.ToSlash(workspace), target, filepath.ToSlash(relative), nil
}

func normalizeToolPathForOS(path string, goos string) (string, error) {
	if goos != "windows" || !strings.HasPrefix(path, "/") {
		return path, nil
	}
	if len(path) >= 2 && isASCIILetter(path[1]) && (len(path) == 2 || path[2] == '/') {
		drivePath := strings.ToUpper(path[1:2]) + `:\`
		if len(path) > 2 {
			drivePath += strings.ReplaceAll(path[3:], "/", `\`)
		}
		return drivePath, nil
	}
	return "", fmt.Errorf("unsupported Unix-style absolute path %q on Windows; use a workspace-relative path or a native Windows absolute path", path)
}

func isASCIILetter(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func skipToolWorkspaceCheckEnabled(ctx context.Context) bool {
	switch strings.ToLower(toolEnvironmentVariable(ctx, toolSkipWorkspaceCheckEnv)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func toolEnvironmentVariable(ctx context.Context, name string) string {
	if environment := core.EnvironmentFromContext(ctx); environment != nil {
		if value, exists := environment[name]; exists {
			return strings.TrimSpace(value)
		}
	}
	return strings.TrimSpace(os.Getenv(name))
}

func joinToolRelativePath(base string, name string) string {
	if base == "." || base == "" {
		return filepath.ToSlash(name)
	}
	return filepath.ToSlash(filepath.Join(base, name))
}
