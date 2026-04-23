package nodes

import (
	"encoding/json"
	"fmt"
	"strings"
	fruntime "weaveflow/runtime"
)

func ensureObjectStateAtPath(root fruntime.State, path string) (fruntime.State, error) {
	segments := fruntime.SplitStatePath(path)
	if len(segments) == 0 {
		return nil, fmt.Errorf("state path is required")
	}

	current := root
	for _, segment := range segments {
		switch typed := current[segment].(type) {
		case nil:
			nested := fruntime.State{}
			current[segment] = nested
			current = nested
		case fruntime.State:
			current = typed
		case map[string]any:
			nested := fruntime.State(typed)
			current[segment] = nested
			current = nested
		default:
			return nil, fmt.Errorf("state path %q contains non-object segment %q (%T)", path, segment, typed)
		}
	}
	return current, nil
}

func stringifyStateValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(value)
		}
		return string(data)
	}
}

func boolStateValue(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		text := strings.TrimSpace(strings.ToLower(typed))
		switch text {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	return false, false
}
