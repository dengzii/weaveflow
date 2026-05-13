package state

import (
	"fmt"
	"reflect"
	"strings"
	"weaveflow/core"
)

func ProjectStateByContract(full State, contract core.NodeIOContract) State {
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

type StatePatchMergeOptions struct {
	Contract       core.NodeIOContract
	ValidateWrites bool
	EnforceWrites  bool
}

func MergeStatePatch(full State, patch StatePatch, options StatePatchMergeOptions) (State, []core.ContractViolation, error) {
	if full == nil {
		full = State{}
	}
	if patch == nil {
		patch = StatePatch{}
	}

	contract := normalizeNodeIOContract(options.Contract)
	merged := full.CloneState()
	applyStatePatchWithContract(merged, State(patch), contract)

	if !options.ValidateWrites || contract.WildcardWrite {
		return merged, nil, nil
	}

	before, err := SnapshotFromState(full)
	if err != nil {
		return nil, nil, err
	}
	after, err := SnapshotFromState(merged)
	if err != nil {
		return nil, nil, err
	}
	changes, err := NewJSONStateCodec("").Diff(before, after)
	if err != nil {
		return nil, nil, err
	}

	violations := ValidateNodeContract("patch", contract, merged, changes)
	for _, required := range contract.RequiredWritePaths {
		if _, ok := resolveSnapshotPathValue(State(patch), required); ok {
			continue
		}
		violations = append(violations, core.ContractViolation{
			NodeID:  "patch",
			Path:    required,
			Kind:    "missing_required_patch_write",
			Message: fmt.Sprintf("patch must write required path %q", required),
		})
	}
	if options.EnforceWrites && len(violations) > 0 {
		return nil, violations, fmt.Errorf("%s", violations[0].Message)
	}

	return merged, violations, nil
}

func MergePatchByContract(full State, patch State, contract core.NodeIOContract) (State, error) {
	merged, _, err := MergeStatePatch(full, StatePatch(patch), StatePatchMergeOptions{
		Contract:       contract,
		ValidateWrites: true,
		EnforceWrites:  true,
	})
	if err != nil {
		return nil, err
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

func collectContractReadPaths(contract core.NodeIOContract) []string {
	seen := map[string]struct{}{}
	paths := make([]string, 0, len(contract.ReadPaths)+len(contract.RequiredReadPaths))
	for _, list := range [][]string{contract.ReadPaths, contract.RequiredReadPaths} {
		for _, path := range list {
			path = NormalizeContractPath(path)
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
	applyStatePatchWithContract(target, patch, core.NodeIOContract{})
}

func applyStatePatchWithContract(target State, patch State, contract core.NodeIOContract) {
	applyStatePatchAt(target, patch, patchApplyOptions{
		mergeStrategies: normalizeMergeStrategies(contract.MergeStrategies),
	}, "")
}

type patchApplyOptions struct {
	mergeStrategies map[string]core.StateMergeStrategy
}

func applyStatePatchAt(target State, patch State, options patchApplyOptions, prefix string) {
	if target == nil || patch == nil {
		return
	}

	for _, key := range []string{stateKeyMessages, stateKeyIterationCount, stateKeyMaxIterations, stateKeyFinalAnswer} {
		value, ok := patch[key]
		if !ok {
			continue
		}
		applyStatePatchValue(target, key, value, statePatchPath(prefix, key, true), options)
	}

	if conversation := conversationSource(patch); conversation != nil {
		for _, key := range []string{stateKeyMessages, stateKeyIterationCount, stateKeyMaxIterations, stateKeyFinalAnswer} {
			value, ok := conversation[key]
			if !ok {
				continue
			}
			applyStatePatchValue(target, key, value, statePatchPath(prefix, key, true), options)
		}
	}

	for scopeName, scopePatch := range patch.Scopes() {
		applyStatePatchAt(target.EnsureScope(scopeName), scopePatch, options, "scopes."+scopeName)
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
			applyStatePatchAt(target.EnsureNamespace(key), namespacePatch, options, namespaceContractPath(key))
			continue
		}

		applyStatePatchValue(target, key, value, statePatchPath(prefix, key, false), options)
	}
}

func applyStatePatchValue(target State, key string, value any, path string, options patchApplyOptions) {
	if target == nil {
		return
	}
	strategy := mergeStrategyForPath(options.mergeStrategies, path)
	if strategy == core.StateMergeAppend {
		existing := target[key]
		if isSpecialStateKey(key) {
			existing, _ = specialStateValue(target, key)
		}
		applyReplaceStatePatchValue(target, key, appendStatePatchValue(existing, value))
		return
	}
	if value == nil {
		if isSpecialStateKey(key) {
			deleteConversationField(target, key)
			return
		}
		delete(target, key)
		return
	}
	if strategy == core.StateMergeReplace {
		applyReplaceStatePatchValue(target, key, value)
		return
	}
	if existing, ok := asStateMap(target[key]); ok {
		if nested, ok := asStateMap(value); ok {
			applyStatePatchAt(existing, nested, options, path)
			target[key] = existing
			return
		}
	}
	applyReplaceStatePatchValue(target, key, value)
}

func applyReplaceStatePatchValue(target State, key string, value any) {
	if isSpecialStateKey(key) {
		applyDecodedGraphValue(target, key, cloneStateValue(value))
		return
	}
	target[key] = cloneStateValue(value)
}

func statePatchPath(prefix string, key string, special bool) string {
	prefix = strings.TrimSpace(prefix)
	key = strings.TrimSpace(key)
	if prefix == "" {
		if special {
			return "conversation." + key
		}
		return "shared." + key
	}
	return prefix + "." + key
}

func namespaceContractPath(key string) string {
	normalized := normalizeStateNamespace(key)
	if normalized == normalizeStateNamespace("runtime") {
		return "runtime"
	}
	return "internal." + normalized
}

func normalizeNodeIOContract(contract core.NodeIOContract) core.NodeIOContract {
	contract = contract.Clone()
	contract.ReadPaths = normalizeContractPathSlice(contract.ReadPaths)
	contract.WritePaths = normalizeContractPathSlice(contract.WritePaths)
	contract.RequiredReadPaths = normalizeContractPathSlice(contract.RequiredReadPaths)
	contract.RequiredWritePaths = normalizeContractPathSlice(contract.RequiredWritePaths)
	contract.MergeStrategies = normalizeMergeStrategies(contract.MergeStrategies)
	return contract
}

func normalizeContractPathSlice(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(paths))
	for _, path := range paths {
		path = NormalizeContractPath(path)
		if path == "" {
			continue
		}
		normalized = append(normalized, path)
	}
	return normalized
}

func normalizeMergeStrategies(strategies map[string]core.StateMergeStrategy) map[string]core.StateMergeStrategy {
	if len(strategies) == 0 {
		return nil
	}
	normalized := make(map[string]core.StateMergeStrategy, len(strategies))
	for path, strategy := range strategies {
		path = NormalizeContractPath(path)
		if path == "" {
			continue
		}
		switch strategy {
		case core.StateMergeReplace, core.StateMergeMerge, core.StateMergeAppend:
			normalized[path] = strategy
		}
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func mergeStrategyForPath(strategies map[string]core.StateMergeStrategy, path string) core.StateMergeStrategy {
	if len(strategies) == 0 {
		return core.StateMergeDefault
	}
	return strategies[NormalizeContractPath(path)]
}

func appendStatePatchValue(existing any, value any) any {
	if existing == nil {
		return cloneStateValue(value)
	}
	if value == nil {
		return cloneStateValue(existing)
	}

	left := reflect.ValueOf(existing)
	if left.Kind() != reflect.Slice {
		return cloneStateValue(value)
	}
	right := reflect.ValueOf(value)
	if right.IsValid() && right.Kind() == reflect.Slice && right.Type() == left.Type() {
		combined := reflect.MakeSlice(left.Type(), left.Len(), left.Len()+right.Len())
		reflect.Copy(combined, left)
		combined = reflect.AppendSlice(combined, right)
		return cloneStateValue(combined.Interface())
	}
	if right.IsValid() && right.Type().AssignableTo(left.Type().Elem()) {
		combined := reflect.MakeSlice(left.Type(), left.Len(), left.Len()+1)
		reflect.Copy(combined, left)
		combined = reflect.Append(combined, right)
		return cloneStateValue(combined.Interface())
	}

	leftItems, ok := anySliceFromValue(existing)
	if !ok {
		return cloneStateValue(value)
	}
	rightItems, ok := anySliceFromValue(value)
	if !ok {
		rightItems = []any{value}
	}
	combined := make([]any, 0, len(leftItems)+len(rightItems))
	for _, item := range leftItems {
		combined = append(combined, cloneStateValue(item))
	}
	for _, item := range rightItems {
		combined = append(combined, cloneStateValue(item))
	}
	return combined
}

func anySliceFromValue(value any) ([]any, bool) {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() || reflected.Kind() != reflect.Slice {
		return nil, false
	}
	items := make([]any, 0, reflected.Len())
	for i := 0; i < reflected.Len(); i++ {
		items = append(items, reflected.Index(i).Interface())
	}
	return items, true
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
