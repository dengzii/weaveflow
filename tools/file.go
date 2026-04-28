package tools

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	fileToolWorkspaceEnv = "WEAVEFLOW_TOOL_WORKDIR"
	defaultReadLimit     = 64 * 1024
	maxReadLimit         = 256 * 1024
	defaultListLimit     = 100
	maxListLimit         = 500
)

type fileOperationRequest struct {
	Action  string `json:"action"`
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

type fileOperationResponse struct {
	Action       string               `json:"action"`
	Path         string               `json:"path"`
	Workspace    string               `json:"workspace"`
	Exists       bool                 `json:"exists,omitempty"`
	IsDir        bool                 `json:"is_dir,omitempty"`
	Size         int64                `json:"size,omitempty"`
	BytesWritten int                  `json:"bytes_written,omitempty"`
	Truncated    bool                 `json:"truncated,omitempty"`
	Content      string               `json:"content,omitempty"`
	Entries      []fileOperationEntry `json:"entries,omitempty"`
}

type fileOperationEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size,omitempty"`
}

func resolveFileOperationPath(path string) (workspace string, target string, relative string, err error) {
	workspace = strings.TrimSpace(os.Getenv(fileToolWorkspaceEnv))
	if workspace == "" {
		workspace, err = os.Getwd()
		if err != nil {
			return "", "", "", err
		}
	}
	workspace, err = filepath.Abs(workspace)
	if err != nil {
		return "", "", "", err
	}

	cleanPath := filepath.Clean(path)
	switch {
	case cleanPath == "":
		return "", "", "", errors.New("file operation path is required")
	case filepath.IsAbs(cleanPath):
		target = cleanPath
	default:
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
		return "", "", "", errors.New("path escapes workspace")
	}

	return filepath.ToSlash(workspace), target, filepath.ToSlash(relative), nil
}

// --- read operations ---

func readFileOperation(workspace string, target string, relativePath string, limit int) (fileOperationResponse, error) {
	info, err := os.Stat(target)
	if err != nil {
		return fileOperationResponse{}, err
	}
	if info.IsDir() {
		return fileOperationResponse{}, errors.New("path is a directory")
	}

	data, err := os.ReadFile(target)
	if err != nil {
		return fileOperationResponse{}, err
	}
	if !utf8.Valid(data) {
		return fileOperationResponse{}, errors.New("file is not a UTF-8 text file")
	}

	limit = normalizeReadLimit(limit)
	truncated := false
	if len(data) > limit {
		data = data[:limit]
		truncated = true
	}

	return fileOperationResponse{
		Action:    "read",
		Path:      relativePath,
		Workspace: workspace,
		Exists:    true,
		Size:      info.Size(),
		Content:   string(data),
		Truncated: truncated,
	}, nil
}

func listFileOperation(workspace string, target string, relativePath string, limit int) (fileOperationResponse, error) {
	info, err := os.Stat(target)
	if err != nil {
		return fileOperationResponse{}, err
	}
	if !info.IsDir() {
		return fileOperationResponse{}, errors.New("path is not a directory")
	}

	items, err := os.ReadDir(target)
	if err != nil {
		return fileOperationResponse{}, err
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Name() < items[j].Name()
	})

	limit = normalizeListLimit(limit)
	entries := make([]fileOperationEntry, 0, min(limit, len(items)))
	for _, item := range items {
		if len(entries) >= limit {
			break
		}
		entry := fileOperationEntry{
			Name:  item.Name(),
			Path:  joinRelativePath(relativePath, item.Name()),
			IsDir: item.IsDir(),
		}
		if info, err := item.Info(); err == nil && !item.IsDir() {
			entry.Size = info.Size()
		}
		entries = append(entries, entry)
	}

	return fileOperationResponse{
		Action:    "list",
		Path:      relativePath,
		Workspace: workspace,
		Exists:    true,
		IsDir:     true,
		Entries:   entries,
	}, nil
}

func statFileOperation(workspace string, target string, relativePath string) (fileOperationResponse, error) {
	info, err := os.Stat(target)
	if err != nil {
		return fileOperationResponse{}, err
	}
	return fileOperationResponse{
		Action:    "stat",
		Path:      relativePath,
		Workspace: workspace,
		Exists:    true,
		IsDir:     info.IsDir(),
		Size:      info.Size(),
	}, nil
}

func existsFileOperation(workspace string, target string, relativePath string) (fileOperationResponse, error) {
	info, err := os.Stat(target)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fileOperationResponse{
				Action:    "exists",
				Path:      relativePath,
				Workspace: workspace,
				Exists:    false,
			}, nil
		}
		return fileOperationResponse{}, err
	}
	return fileOperationResponse{
		Action:    "exists",
		Path:      relativePath,
		Workspace: workspace,
		Exists:    true,
		IsDir:     info.IsDir(),
		Size:      info.Size(),
	}, nil
}

// --- write operations ---

func writeFileOperation(workspace string, target string, relativePath string, content string, appendMode bool) (fileOperationResponse, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fileOperationResponse{}, err
	}

	action := "write"
	if appendMode {
		action = "append"
	}

	if appendMode {
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return fileOperationResponse{}, err
		}
		defer func() {
			_ = f.Close()
		}()
		if _, err := f.WriteString(content); err != nil {
			return fileOperationResponse{}, err
		}
	} else {
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			return fileOperationResponse{}, err
		}
	}

	info, err := os.Stat(target)
	if err != nil {
		return fileOperationResponse{}, err
	}

	return fileOperationResponse{
		Action:       action,
		Path:         relativePath,
		Workspace:    workspace,
		Exists:       true,
		Size:         info.Size(),
		BytesWritten: len(content),
	}, nil
}

func mkdirFileOperation(workspace string, target string, relativePath string) (fileOperationResponse, error) {
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fileOperationResponse{}, err
	}
	return fileOperationResponse{
		Action:    "mkdir",
		Path:      relativePath,
		Workspace: workspace,
		Exists:    true,
		IsDir:     true,
	}, nil
}

// --- helpers ---

func normalizeReadLimit(limit int) int {
	switch {
	case limit <= 0:
		return defaultReadLimit
	case limit > maxReadLimit:
		return maxReadLimit
	default:
		return limit
	}
}

func normalizeListLimit(limit int) int {
	switch {
	case limit <= 0:
		return defaultListLimit
	case limit > maxListLimit:
		return maxListLimit
	default:
		return limit
	}
}

func joinRelativePath(base string, name string) string {
	if base == "." || base == "" {
		return filepath.ToSlash(name)
	}
	return filepath.ToSlash(filepath.Join(base, name))
}
