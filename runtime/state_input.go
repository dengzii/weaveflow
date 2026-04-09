package runtime

import (
	"encoding/json"
	"fmt"
	"strings"
)

func NormalizeInputState(input State) (State, error) {
	if len(input) == 0 {
		return State{}, nil
	}
	if err := validateInputStateKeys(input, "$"); err != nil {
		return nil, err
	}

	snapshot := StateSnapshot{
		Version: DefaultStateVersion,
		Shared:  GraphState{},
	}

	for key, value := range input {
		handled := false
		for _, extension := range defaultStateExtensions() {
			ok, err := extension.NormalizeInputStateField(&snapshot, key, value)
			if err != nil {
				return nil, err
			}
			if ok {
				handled = true
				break
			}
		}
		if handled {
			continue
		}

		if err := validatePersistableStateValue(key, value); err != nil {
			return nil, err
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode input field %q: %w", key, err)
		}
		snapshot.Shared[key] = raw
	}

	if len(snapshot.Shared) == 0 {
		snapshot.Shared = nil
	}
	return StateFromSnapshot(snapshot)
}

func validateInputStateKeys(input State, location string) error {
	for key := range input {
		if !isReservedInputStateKey(key) {
			continue
		}
		return fmt.Errorf("input state key %q at %s is reserved", key, normalizeStatePath(location))
	}
	return nil
}

func isReservedInputStateKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	switch key {
	case resumeInputScopesKey, stateNamespaceScopes:
		return true
	default:
		return strings.HasPrefix(key, stateNamespacePrefix)
	}
}
