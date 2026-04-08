package runtime

import (
	"strconv"
	"strings"
)

// ResolveStatePath walks a dot-delimited path from the provided root state.
// Path segments can address nested maps and slice indexes.
func ResolveStatePath(state State, path string) (any, bool) {
	return ResolveStateValue(state, SplitStatePath(path))
}

func ResolveStateValue(root any, segments []string) (any, bool) {
	if len(segments) == 0 {
		return nil, false
	}

	value := root
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			return nil, false
		}

		next, ok := resolveStatePathSegment(value, segment)
		if !ok {
			return nil, false
		}
		value = next
	}
	return value, true
}

func SplitStatePath(path string) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}

	segments := strings.Split(path, ".")
	for i := range segments {
		segments[i] = strings.TrimSpace(segments[i])
	}
	return segments
}

func resolveStatePathSegment(current any, segment string) (any, bool) {
	switch typed := current.(type) {
	case nil:
		return nil, false
	case State:
		next, ok := typed[segment]
		return next, ok
	case map[string]any:
		next, ok := typed[segment]
		return next, ok
	case []any:
		index, ok := resolveStateSliceIndex(segment, len(typed))
		if !ok {
			return nil, false
		}
		return typed[index], true
	case []map[string]any:
		index, ok := resolveStateSliceIndex(segment, len(typed))
		if !ok {
			return nil, false
		}
		return typed[index], true
	case []string:
		index, ok := resolveStateSliceIndex(segment, len(typed))
		if !ok {
			return nil, false
		}
		return typed[index], true
	default:
		return nil, false
	}
}

func resolveStateSliceIndex(segment string, size int) (int, bool) {
	index, err := strconv.Atoi(segment)
	if err != nil || index < 0 || index >= size {
		return 0, false
	}
	return index, true
}
