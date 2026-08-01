package server

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

var errInvalidRequest = errors.New("invalid request")

func invalidRequestf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errInvalidRequest, fmt.Sprintf(format, args...))
}

func requirePathParam(c *gin.Context, name string) (string, bool) {
	value := optionalPathParam(c, name)
	if value == "" {
		writeError(c, 400, invalidRequestf("%s is required", name))
		return "", false
	}
	return value, true
}

func optionalPathParam(c *gin.Context, name string) string {
	return strings.TrimSpace(c.Param(name))
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

func stringListQuery(c *gin.Context, name string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, value := range c.QueryArray(name) {
		for _, item := range strings.Split(value, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if _, exists := seen[item]; exists {
				continue
			}
			seen[item] = struct{}{}
			result = append(result, item)
		}
	}
	return result
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
