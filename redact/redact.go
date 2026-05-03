package redact

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

const Placeholder = "[REDACTED]"

func Path(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}

	base := filepath.Base(path)
	if strings.TrimSpace(base) == "" || base == "." || base == string(filepath.Separator) {
		return Placeholder
	}
	return base
}

func Text(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}

	lines := strings.Split(text, "\n")
	changed := false
	for i, line := range lines {
		if containsSensitiveMarker(strings.ToLower(line)) {
			lines[i] = Placeholder
			changed = true
		}
	}
	if !changed {
		return text
	}
	return strings.Join(lines, "\n")
}

func JSONString(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	var value any
	if err := json.Unmarshal([]byte(raw), &value); err == nil {
		if encoded, err := json.Marshal(Any(value)); err == nil {
			return string(encoded)
		}
	}

	return Text(raw)
}

func Any(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for key, item := range typed {
			if isSensitiveKey(key) {
				redacted[key] = Placeholder
				continue
			}
			if isPathKey(key) {
				if text, ok := item.(string); ok {
					redacted[key] = Path(text)
					continue
				}
			}
			redacted[key] = Any(item)
		}
		return redacted
	case []any:
		items := make([]any, len(typed))
		for i, item := range typed {
			items[i] = Any(item)
		}
		return items
	case []string:
		items := make([]string, len(typed))
		for i, item := range typed {
			items[i] = Text(item)
		}
		return items
	case string:
		return Text(typed)
	default:
		return value
	}
}

func isSensitiveKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "-", "_")

	switch key {
	case "authorization", "auth_header", "api_key", "x_api_key", "access_token", "refresh_token", "password", "secret", "cookie", "set_cookie", "private_key":
		return true
	default:
		return false
	}
}

func isPathKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "-", "_")

	switch key {
	case "path", "model_path", "file_path", "location", "payload_ref":
		return true
	default:
		return false
	}
}

func containsSensitiveMarker(line string) bool {
	for _, marker := range []string{
		"authorization:",
		"bearer ",
		"x-api-key",
		"api-key:",
		"apikey:",
		"access_token",
		"refresh_token",
		"password=",
		"password:",
		"secret=",
		"secret:",
		"cookie:",
		"set-cookie:",
		"session=",
		"token=",
		"private_key",
		"-----begin",
	} {
		if strings.Contains(line, marker) {
			return true
		}
	}
	return false
}
