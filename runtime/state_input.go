package runtime

import (
	"encoding/json"
	"fmt"
)

func NormalizeInputState(input State) (State, error) {
	if len(input) == 0 {
		return State{}, nil
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
