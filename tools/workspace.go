package tools

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const (
	toolWorkspaceEnv          = "WEAVEFLOW_TOOL_WORKDIR"
	toolSkipWorkspaceCheckEnv = "WEAVEFLOW_TOOL_SKIP_WORKSPACE_CHECK"
)

func toolWorkspaceDir() (string, error) {
	dir := strings.TrimSpace(os.Getenv(toolWorkspaceEnv))
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	return filepath.Abs(dir)
}

func resolveToolPath(path string) (workspace string, target string, relative string, err error) {
	if strings.TrimSpace(path) == "" {
		return "", "", "", errors.New("tool path is required")
	}

	workspace, err = toolWorkspaceDir()
	if err != nil {
		return "", "", "", err
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
		if !skipToolWorkspaceCheckEnabled() {
			return "", "", "", errors.New("path escapes workspace")
		}
		relative = filepath.ToSlash(target)
	}

	return filepath.ToSlash(workspace), target, filepath.ToSlash(relative), nil
}

func skipToolWorkspaceCheckEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(toolSkipWorkspaceCheckEnv))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func joinToolRelativePath(base string, name string) string {
	if base == "." || base == "" {
		return filepath.ToSlash(name)
	}
	return filepath.ToSlash(filepath.Join(base, name))
}
