package runtime

import "encoding/json"

type StateExtension interface {
	FieldDefinitions() []StateFieldDefinition
	IsSpecialKey(key string) bool
	ExtractRootSnapshot(state State, snapshot *StateSnapshot) error
	ApplyRootSnapshot(state State, snapshot StateSnapshot) error
	EncodeScopedState(values map[string]any, target GraphState) error
	DecodeStateField(target State, key string, value any) bool
	NormalizeInputStateField(snapshot *StateSnapshot, key string, value any) (bool, error)
	AppendSnapshotFields(snapshot StateSnapshot, result map[string]json.RawMessage) error
}

func defaultStateExtensions() []StateExtension {
	return []StateExtension{
		conversationExtension{},
	}
}

func isSpecialStateKey(key string) bool {
	for _, extension := range defaultStateExtensions() {
		if extension.IsSpecialKey(key) {
			return true
		}
	}
	return false
}
