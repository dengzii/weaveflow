package runtime

import (
	"fmt"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

func validatePersistableStateValue(path string, value any) error {
	switch typed := value.(type) {
	case nil:
		return nil
	case bool, string,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return nil
	case State:
		return validatePersistableStateMap(path, typed)
	case map[string]any:
		return validatePersistableStateMap(path, typed)
	case []any:
		for i, item := range typed {
			if err := validatePersistableStateValue(statePathIndex(path, i), item); err != nil {
				return err
			}
		}
		return nil
	case []string:
		return nil
	case []map[string]any:
		for i, item := range typed {
			if err := validatePersistableStateMap(statePathIndex(path, i), State(item)); err != nil {
				return err
			}
		}
		return nil
	case []llms.MessageContent:
		return nil
	default:
		return fmt.Errorf(
			"unsupported state value at %q: %T (supported: primitives, map[string]any, []any, []string, []map[string]any, []llms.MessageContent)",
			normalizeStatePath(path),
			value,
		)
	}
}

func validatePersistableStateMap(path string, values State) error {
	for key, value := range values {
		if err := validatePersistableStateValue(statePathKey(path, key), value); err != nil {
			return err
		}
	}
	return nil
}

func statePathKey(base string, key string) string {
	if base == "" {
		return key
	}
	return base + "." + key
}

func statePathIndex(base string, index int) string {
	return fmt.Sprintf("%s[%d]", normalizeStatePath(base), index)
}

func normalizeStatePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "$"
	}
	return path
}
