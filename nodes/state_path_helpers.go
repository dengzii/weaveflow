package nodes

import (
	"strings"

	wfstate "weaveflow/state"
)

func stateObjectAtPath(state wfstate.State, path string) wfstate.State {
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
	case wfstate.State:
		return typed
	case map[string]any:
		return wfstate.State(typed)
	default:
		return nil
	}
}
