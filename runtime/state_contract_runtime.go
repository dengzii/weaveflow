package runtime

import (
	"fmt"
	"reflect"
	"strings"
)

func ProjectStateByContract(full State, contract NodeIOContract) State {
	if full == nil {
		full = State{}
	}
	if contract.WildcardRead {
		return full.CloneState()
	}

	projected := State{}
	for _, path := range collectContractReadPaths(contract) {
		value, ok := resolveSnapshotPathValue(full, path)
		if !ok {
			continue
		}
		setSnapshotPathValue(projected, path, cloneStateValue(value))
	}
	return projected
}

func ResolveContractPathValue(state State, path string) (any, bool) {
	return resolveSnapshotPathValue(state, NormalizeContractPath(path))
}

func SetContractPathValue(state State, path string, value any) {
	setSnapshotPathValue(state, NormalizeContractPath(path), value)
}

func MergePatchByContract(full State, patch State, contract NodeIOContract) (State, error) {
	if full == nil {
		full = State{}
	}
	if patch == nil {
		patch = State{}
	}

	merged := full.CloneState()
	applyStatePatch(merged, patch)

	before, err := SnapshotFromState(full)
	if err != nil {
		return nil, err
	}
	after, err := SnapshotFromState(merged)
	if err != nil {
		return nil, err
	}
	changes, err := NewJSONStateCodec("").Diff(before, after)
	if err != nil {
		return nil, err
	}

	if !contract.WildcardWrite {
		violations := ValidateNodeContract("patch", contract, merged, changes)
		if len(violations) > 0 {
			return nil, fmt.Errorf("%s", violations[0].Message)
		}

		for _, required := range contract.RequiredWritePaths {
			if _, ok := resolveSnapshotPathValue(patch, required); ok {
				continue
			}
			return nil, fmt.Errorf("patch must write required path %q", required)
		}
	}

	return merged, nil
}

func DiffState(before State, after State) (State, error) {
	if before == nil {
		before = State{}
	}
	if after == nil {
		after = State{}
	}

	beforeSnapshot, err := SnapshotFromState(before)
	if err != nil {
		return nil, err
	}
	afterSnapshot, err := SnapshotFromState(after)
	if err != nil {
		return nil, err
	}
	changes, err := NewJSONStateCodec("").Diff(beforeSnapshot, afterSnapshot)
	if err != nil {
		return nil, err
	}

	patch := State{}
	for _, change := range changes {
		if len(change.After) == 0 {
			setSnapshotPathValue(patch, change.Path, nil)
			continue
		}

		value, ok := resolveSnapshotPathValue(after, change.Path)
		if !ok {
			setSnapshotPathValue(patch, change.Path, nil)
			continue
		}
		setSnapshotPathValue(patch, change.Path, cloneStateValue(value))
	}
	return patch, nil
}

func collectContractReadPaths(contract NodeIOContract) []string {
	seen := map[string]struct{}{}
	paths := make([]string, 0, len(contract.ReadPaths)+len(contract.RequiredReadPaths))
	for _, list := range [][]string{contract.ReadPaths, contract.RequiredReadPaths} {
		for _, path := range list {
			path = strings.TrimSpace(path)
			if path == "" {
				continue
			}
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			paths = append(paths, path)
		}
	}
	return paths
}

func applyStatePatch(target State, patch State) {
	if target == nil || patch == nil {
		return
	}

	for _, key := range []string{stateKeyMessages, stateKeyIterationCount, stateKeyMaxIterations, stateKeyFinalAnswer} {
		value, ok := patch[key]
		if !ok {
			continue
		}
		if value == nil {
			deleteConversationField(target, key)
			continue
		}
		applyDecodedGraphValue(target, key, cloneStateValue(value))
	}

	if conversation := conversationSource(patch); conversation != nil {
		copyConversationState(target, conversation)
	}

	for scopeName, scopePatch := range patch.scopes() {
		applyStatePatch(target.EnsureScope(scopeName), scopePatch)
	}

	for key, value := range patch {
		if isInfrastructureStateKey(key) || isSpecialStateKey(key) {
			continue
		}
		if isInternalSnapshotNamespaceKey(key) {
			namespacePatch, ok := asStateMap(value)
			if !ok {
				if value == nil {
					delete(target, key)
				}
				continue
			}
			applyStatePatch(target.EnsureNamespace(key), namespacePatch)
			continue
		}

		if value == nil {
			delete(target, key)
			continue
		}
		if existing, ok := asStateMap(target[key]); ok {
			if nested, ok := asStateMap(value); ok {
				applyStatePatch(existing, nested)
				target[key] = existing
				continue
			}
		}
		target[key] = cloneStateValue(value)
	}
}

func resolveSnapshotPathValue(state State, path string) (any, bool) {
	segments := SplitStatePath(path)
	if len(segments) == 0 {
		return nil, false
	}

	switch segments[0] {
	case "shared":
		return resolveStateSegments(state, segments[1:])
	case "scopes":
		if len(segments) < 2 {
			return nil, false
		}
		scopeState := state.Scope(segments[1])
		if len(segments) == 2 {
			if scopeState == nil {
				return nil, false
			}
			return scopeState.CloneState(), true
		}
		if value, ok := resolveScopedSpecialValue(state, scopeState, segments[2]); ok {
			if len(segments) == 3 {
				return cloneStateValue(value), true
			}
			return ResolveStateValue(value, segments[3:])
		}
		if scopeState == nil {
			return nil, false
		}
		return resolveStateSegments(scopeState, segments[2:])
	case "internal":
		if len(segments) < 2 {
			return nil, false
		}
		namespace := state.Namespace(segments[1])
		if namespace == nil {
			return nil, false
		}
		return resolveStateSegments(namespace, segments[2:])
	case "conversation":
		return resolveConversationSegments(state, segments[1:])
	case "runtime":
		namespace := state.Namespace("runtime")
		if namespace == nil {
			return nil, false
		}
		return resolveStateSegments(namespace, segments[1:])
	default:
		return resolveStateSegments(state, segments)
	}
}

func resolveScopedSpecialValue(root State, scopeState State, key string) (any, bool) {
	switch key {
	case stateKeyMessages:
		if messages, ok := conversationMessages(conversationSource(scopeState)); ok {
			return messages, true
		}
		if messages, ok := conversationMessages(conversationSource(root)); ok {
			return messages, true
		}
	case stateKeyMaxIterations:
		if value, ok := conversationInt(conversationSource(scopeState), key); ok {
			return value, true
		}
		if value, ok := conversationInt(conversationSource(root), key); ok {
			return value, true
		}
	case stateKeyIterationCount:
		if value, ok := conversationInt(conversationSource(scopeState), key); ok {
			return value, true
		}
	case stateKeyFinalAnswer:
		if value, ok := conversationString(conversationSource(scopeState), key); ok {
			return value, true
		}
	}
	return nil, false
}

func resolveStateSegments(state State, segments []string) (any, bool) {
	if len(segments) == 0 {
		return state.CloneState(), true
	}
	if value, ok := specialStateValue(state, segments[0]); ok {
		if len(segments) == 1 {
			return cloneStateValue(value), true
		}
		return ResolveStateValue(value, segments[1:])
	}
	return ResolveStateValue(state, segments)
}

func resolveConversationSegments(state State, segments []string) (any, bool) {
	if len(segments) == 0 {
		value := conversationStateMap(state)
		if len(value) == 0 {
			return nil, false
		}
		return value, true
	}
	value, ok := specialStateValue(state, segments[0])
	if !ok {
		return nil, false
	}
	if len(segments) == 1 {
		return cloneStateValue(value), true
	}
	return ResolveStateValue(value, segments[1:])
}

func setSnapshotPathValue(target State, path string, value any) {
	segments := SplitStatePath(path)
	if len(segments) == 0 {
		return
	}

	switch segments[0] {
	case "shared":
		setStateSegments(target, segments[1:], value)
	case "scopes":
		if len(segments) < 2 {
			return
		}
		scope := target.EnsureScope(segments[1])
		setStateSegments(scope, segments[2:], value)
	case "internal":
		if len(segments) < 2 {
			return
		}
		namespace := target.EnsureNamespace(segments[1])
		setStateSegments(namespace, segments[2:], value)
	case "conversation":
		setConversationSegments(target, segments[1:], value)
	case "runtime":
		namespace := target.EnsureNamespace("runtime")
		setStateSegments(namespace, segments[1:], value)
	default:
		setStateSegments(target, segments, value)
	}
}

func setStateSegments(target State, segments []string, value any) {
	if target == nil {
		return
	}
	if len(segments) == 0 {
		nested, ok := asStateMap(value)
		if !ok {
			return
		}
		mergeStateMap(target, nested)
		return
	}
	if len(segments) == 1 {
		key := segments[0]
		if isSpecialStateKey(key) {
			applyDecodedGraphValue(target, key, cloneStateValue(value))
			if value == nil {
				target[key] = nil
			}
			return
		}
		target[key] = cloneStateValue(value)
		return
	}

	current := target.Ensure(segments[0])
	setStateSegments(current, segments[1:], value)
}

func setConversationSegments(target State, segments []string, value any) {
	if target == nil {
		return
	}
	if len(segments) == 0 {
		conversationState, ok := asStateMap(value)
		if !ok {
			return
		}
		for key, item := range conversationState {
			setConversationSegments(target, []string{key}, item)
		}
		return
	}
	if len(segments) == 1 {
		setStateSegments(target, segments, value)
		return
	}

	current, ok := specialStateValue(target, segments[0])
	if !ok {
		return
	}
	switch typed := current.(type) {
	case State:
		setStateSegments(typed, segments[1:], value)
	case map[string]any:
		setStateSegments(State(typed), segments[1:], value)
	case []any:
		index, ok := resolveStateSliceIndex(segments[1], len(typed))
		if !ok {
			return
		}
		if len(segments) == 2 {
			typed[index] = cloneStateValue(value)
		}
	}
}

func specialStateValue(state State, key string) (any, bool) {
	switch key {
	case stateKeyMessages:
		messages := state.Conversation("").Messages()
		if messages == nil {
			return nil, false
		}
		return messages, true
	case stateKeyIterationCount:
		if _, ok := conversationInt(conversationSource(state), key); !ok {
			return nil, false
		}
		return state.Conversation("").IterationCount(), true
	case stateKeyMaxIterations:
		if _, ok := conversationInt(conversationSource(state), key); !ok {
			return nil, false
		}
		return state.Conversation("").MaxIterations(), true
	case stateKeyFinalAnswer:
		value := state.Conversation("").FinalAnswer()
		if value == "" {
			if _, ok := conversationString(conversationSource(state), key); !ok {
				return nil, false
			}
		}
		return value, true
	default:
		return nil, false
	}
}

func conversationStateMap(state State) State {
	result := State{}
	if messages, ok := specialStateValue(state, stateKeyMessages); ok {
		result[stateKeyMessages] = cloneStateValue(messages)
	}
	if iterationCount, ok := specialStateValue(state, stateKeyIterationCount); ok {
		result[stateKeyIterationCount] = iterationCount
	}
	if maxIterations, ok := specialStateValue(state, stateKeyMaxIterations); ok {
		result[stateKeyMaxIterations] = maxIterations
	}
	if answer, ok := specialStateValue(state, stateKeyFinalAnswer); ok {
		result[stateKeyFinalAnswer] = answer
	}
	return result
}

func deleteConversationField(state State, key string) {
	if state == nil {
		return
	}
	conversation := conversationSource(state)
	if conversation == nil {
		return
	}
	delete(conversation, key)
}

func statesEqual(left, right any) bool {
	return reflect.DeepEqual(left, right)
}
