package registry

import (
	"strconv"
	"strings"
)

func StringConfig(config map[string]any, key string) string {
	if len(config) == 0 {
		return ""
	}
	if value, ok := config[key].(string); ok {
		return value
	}
	return ""
}

func StringConfigTrim(config map[string]any, key string) string {
	return strings.TrimSpace(StringConfig(config, key))
}

func StringSliceConfig(config map[string]any, key string) []string {
	if len(config) == 0 {
		return nil
	}
	raw, ok := config[key]
	if !ok {
		return nil
	}
	if values, ok := raw.([]any); ok {
		result := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				result = append(result, text)
			}
		}
		return result
	}
	if typed, ok := raw.([]string); ok {
		return append([]string(nil), typed...)
	}
	return nil
}

func StringSliceConfigTrim(config map[string]any, key string) []string {
	if len(config) == 0 {
		return nil
	}
	raw, ok := config[key]
	if !ok {
		return nil
	}
	collect := func(values []string) []string {
		out := make([]string, 0, len(values))
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value != "" {
				out = append(out, value)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	switch typed := raw.(type) {
	case []string:
		return collect(typed)
	case []any:
		values := make([]string, 0, len(typed))
		for _, value := range typed {
			text, _ := value.(string)
			values = append(values, text)
		}
		return collect(values)
	}
	return nil
}

func MapStringConfig(config map[string]any, key string) map[string]string {
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

func IntConfig(config map[string]any, key string) (int, bool) {
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

func BoolConfig(config map[string]any, key string) (bool, bool) {
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

func FloatConfig(config map[string]any, key string) (float64, bool) {
	if len(config) == 0 {
		return 0, false
	}

	switch value := config[key].(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	}

	return 0, false
}
