package nodes

import (
	"fmt"
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

func ensureStateObjectAtPath(root wfstate.State, path string) (wfstate.State, error) {
	segments := wfstate.SplitStatePath(path)
	if len(segments) == 0 {
		return nil, fmt.Errorf("state object path is required")
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
			return nil, fmt.Errorf("state object path %q contains non-object segment %q (%T)", path, segment, typed)
		}
	}
	return current, nil
}
