package registry

import (
	"strconv"
	"strings"
)

func stringConfig(config map[string]any, key string) string {
	if len(config) == 0 {
		return ""
	}
	if value, ok := config[key].(string); ok {
		return value
	}
	return ""
}

func mapStringConfig(config map[string]any, key string) map[string]string {
	if len(config) == 0 {
		return nil
	}
	raw, ok := config[key]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case map[string]string:
		result := make(map[string]string, len(typed))
		for k, v := range typed {
			result[k] = v
		}
		return result
	case map[string]any:
		result := make(map[string]string, len(typed))
		for k, v := range typed {
			if s, ok := v.(string); ok {
				result[k] = s
			}
		}
		return result
	}
	return nil
}

func intConfig(config map[string]any, key string) (int, bool) {
	if len(config) == 0 {
		return 0, false
	}

	switch value := config[key].(type) {
	case int:
		return value, true
	case int8:
		return int(value), true
	case int16:
		return int(value), true
	case int32:
		return int(value), true
	case int64:
		return int(value), true
	case float32:
		return int(value), true
	case float64:
		return int(value), true
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil {
			return parsed, true
		}
	}

	return 0, false
}

func boolConfig(config map[string]any, key string) (bool, bool) {
	if len(config) == 0 {
		return false, false
	}

	switch value := config[key].(type) {
	case bool:
		return value, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		if err == nil {
			return parsed, true
		}
	}

	return false, false
}
