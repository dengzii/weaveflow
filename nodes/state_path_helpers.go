package nodes

import (
	"strings"

	fruntime "weaveflow/runtime"
)

func stateObjectAtPath(state fruntime.State, path string) fruntime.State {
	if state == nil {
		return nil
	}

	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}

	value, ok := state.ResolvePath(path)
	if !ok {
		return nil
	}

	switch typed := value.(type) {
	case fruntime.State:
		return typed
	case map[string]any:
		return fruntime.State(typed)
	default:
		return nil
	}
}
