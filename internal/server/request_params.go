package server

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

var errInvalidRequest = errors.New("invalid request")

func invalidRequestf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errInvalidRequest, fmt.Sprintf(format, args...))
}

func requirePathParam(c *gin.Context, name string) (string, bool) {
	rawValue := strings.TrimSpace(c.Param(name))
	if rawValue == "" {
		writeError(c, 400, invalidRequestf("%s is required", name))
		return "", false
	}
	value := filepath.Base(rawValue)
	if !filepath.IsLocal(rawValue) || rawValue == ".." || strings.Contains(rawValue, "../") || strings.Contains(rawValue, `..\`) || strings.Contains(rawValue, "/") || strings.Contains(rawValue, "\\") || value != rawValue {
		writeError(c, 400, invalidRequestf("%s must be a single path segment", name))
		return "", false
	}
	return value, true
}

func optionalStringQuery(c *gin.Context, name string) (string, error) {
	values, exists := c.GetQueryArray(name)
	if !exists {
		return "", nil
	}
	if len(values) != 1 {
		return "", invalidRequestf("%s must be specified once", name)
	}
	value := strings.TrimSpace(values[0])
	if value == "" {
		return "", invalidRequestf("%s must not be empty", name)
	}
	return value, nil
}

func stringListQuery(c *gin.Context, name string) ([]string, error) {
	values, exists := c.GetQueryArray(name)
	if !exists {
		return nil, nil
	}
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				return nil, invalidRequestf("%s must not contain empty values", name)
			}
			if _, exists := seen[item]; exists {
				continue
			}
			seen[item] = struct{}{}
			result = append(result, item)
		}
	}
	return result, nil
}

func requireRecordIDPathParam(c *gin.Context, name string) (string, bool) {
	rawValue := c.Param(name)
	if strings.TrimSpace(rawValue) == "" {
		writeError(c, 400, invalidRequestf("%s is required", name))
		return "", false
	}
	value := filepath.Base(rawValue)
	if !filepath.IsLocal(rawValue) || rawValue == ".." || strings.Contains(rawValue, "../") || strings.Contains(rawValue, `..\`) || strings.Contains(rawValue, "/") || strings.Contains(rawValue, "\\") || value != rawValue {
		writeError(c, 400, invalidRequestf("%s must be a single path segment", name))
		return "", false
	}
	if !isPortableRecordID(value) {
		writeError(c, 400, invalidRequestf("%s must be a portable record ID", name))
		return "", false
	}
	return filepath.Base(value), true
}

func requireGraphIDPathParam(c *gin.Context) (string, bool) {
	return requireRecordIDPathParam(c, "graph_id")
}

func isPortableRecordID(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || value == "." || value == ".." || len(value) > 200 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '-' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func positiveIntQuery(c *gin.Context, name string, defaultValue, maximum int) (int, error) {
	value, err := optionalStringQuery(c, name)
	if err != nil {
		return 0, err
	}
	if value == "" {
		return defaultValue, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, invalidRequestf("%s must be a positive integer", name)
	}
	if maximum > 0 && parsed > maximum {
		return 0, invalidRequestf("%s must not exceed %d", name, maximum)
	}
	return parsed, nil
}
