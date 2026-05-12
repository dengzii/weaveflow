package nodes

import (
	"encoding/json"
	"fmt"
	"strings"
	wfstate "weaveflow/state"
)

func ensureObjectStateAtPath(root wfstate.State, path string) (wfstate.State, error) {
	segments := wfstate.SplitStatePath(path)
	if len(segments) == 0 {
		return nil, fmt.Errorf("state path is required")
	}

	current := root
	for _, segment := range segments {
		switch typed := current[segment].(type) {
		case nil:
			nested := wfstate.State{}
			current[segment] = nested
			current = nested
		case wfstate.State:
			current = typed
		case map[string]any:
			nested := wfstate.State(typed)
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
