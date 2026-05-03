package neo

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const cacheFilePreviewLimit = 128 * 1024

type CacheFilesResponse struct {
	CacheDir string           `json:"cache_dir"`
	Files    []CacheFileEntry `json:"files"`
}

type CacheFileEntry struct {
	Path          string    `json:"path"`
	Name          string    `json:"name"`
	Size          int64     `json:"size"`
	ModifiedAt    time.Time `json:"modified_at"`
	ContentType   string    `json:"content_type,omitempty"`
	IsText        bool      `json:"is_text"`
	IsPreviewable bool      `json:"is_previewable"`
}

type CacheFileDetail struct {
	CacheDir    string    `json:"cache_dir"`
	Path        string    `json:"path"`
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	ModifiedAt  time.Time `json:"modified_at"`
	ContentType string    `json:"content_type,omitempty"`
	Encoding    string    `json:"encoding"`
	IsText      bool      `json:"is_text"`
	Truncated   bool      `json:"truncated,omitempty"`
	Content     string    `json:"content,omitempty"`
}

func listCacheFiles(ctx context.Context, baseDir string) ([]CacheFileEntry, error) {
	files := make([]CacheFileEntry, 0, 256)
	err := filepath.WalkDir(baseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(baseDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		contentType, isText := detectFileType(path)
		files = append(files, CacheFileEntry{
			Path:          rel,
			Name:          filepath.Base(path),
			Size:          info.Size(),
			ModifiedAt:    info.ModTime(),
			ContentType:   contentType,
			IsText:        isText,
			IsPreviewable: isText || info.Size() == 0,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
	return files, nil
}

func loadCacheFileDetail(baseDir, relPath string) (*CacheFileDetail, error) {
	relPath = strings.TrimSpace(relPath)
	if relPath == "" {
		return nil, fmt.Errorf("file path is required")
	}

	fullPath, err := resolveCacheFilePath(baseDir, relPath)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("file %q not found", relPath)
		}
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("file %q is a directory", relPath)
	}

	contentType, isText := detectFileType(fullPath)
	raw, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, err
	}

	detail := &CacheFileDetail{
		CacheDir:    baseDir,
		Path:        filepath.ToSlash(relPath),
		Name:        filepath.Base(fullPath),
		Size:        info.Size(),
		ModifiedAt:  info.ModTime(),
		ContentType: contentType,
		IsText:      isText,
	}

	preview := raw
	if len(preview) > cacheFilePreviewLimit {
		preview = preview[:cacheFilePreviewLimit]
		detail.Truncated = true
	}

	if isText {
		detail.Encoding = "utf-8"
		detail.Content = string(preview)
		return detail, nil
	}

	detail.Encoding = "base64"
	detail.Content = base64.StdEncoding.EncodeToString(preview)
	return detail, nil
}

func resolveCacheFilePath(baseDir, relPath string) (string, error) {
	cleanRel := filepath.Clean(filepath.FromSlash(relPath))
	if cleanRel == "." || cleanRel == string(filepath.Separator) {
		return "", fmt.Errorf("file path is required")
	}

	fullPath := filepath.Join(baseDir, cleanRel)
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", err
	}

	rel, err := filepath.Rel(absBase, absPath)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("file path escapes cache_dir")
	}
	return absPath, nil
}

func detectFileType(path string) (string, bool) {
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if contentType == "" {
		file, err := os.Open(path)
		if err == nil {
			defer file.Close()
			buf := make([]byte, 512)
			n, _ := file.Read(buf)
			contentType = http.DetectContentType(buf[:n])
		}
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	isText := strings.HasPrefix(contentType, "text/") ||
		strings.Contains(contentType, "json") ||
		strings.Contains(contentType, "xml") ||
		strings.Contains(contentType, "yaml") ||
		strings.Contains(contentType, "javascript")
	if isText {
		return contentType, true
	}

	file, err := os.Open(path)
	if err != nil {
		return contentType, false
	}
	defer file.Close()

	buf := make([]byte, 2048)
	n, _ := file.Read(buf)
	buf = buf[:n]
	if bytes.IndexByte(buf, 0) >= 0 {
		return contentType, false
	}
	return contentType, utf8.Valid(buf)
}
