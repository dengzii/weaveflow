package runtime

import "fmt"

const resumeInputScopesKey = "scopes"

func mergeResumeInput(base State, input State) (State, error) {
	if len(input) == 0 {
		if base == nil {
			return State{}, nil
		}
		return base, nil
	}

	merged := State{}
	if base != nil {
		merged = base.CloneState()
	}

	rootInput := State{}
	for key, value := range input {
		switch key {
		case resumeInputScopesKey, stateNamespaceScopes:
			if err := mergeResumeScopes(merged, value); err != nil {
				return nil, err
			}
		default:
			rootInput[key] = value
		}
	}

	if len(rootInput) == 0 {
		return merged, nil
	}

	normalized, err := NormalizeInputState(rootInput)
	if err != nil {
		return nil, err
	}
	mergeStateMap(merged, normalized)
	return merged, nil
}

func mergeResumeScopes(target State, raw any) error {
	scopes, ok := asStateMap(raw)
	if !ok {
		return fmt.Errorf("resume input scopes must be a map[string]any, got %T", raw)
	}
	for scopeName, value := range scopes {
		scopeInput, err := normalizeResumeScopeInput(scopeName, value)
		if err != nil {
			return err
		}
		mergeStateMap(target.EnsureScope(scopeName), scopeInput)
	}
	return nil
}

func normalizeResumeScopeInput(scopeName string, raw any) (State, error) {
	input, ok := asStateMap(raw)
	if !ok {
		return nil, fmt.Errorf("resume input scope %q must be a map[string]any, got %T", scopeName, raw)
	}
	normalized, err := NormalizeInputState(input)
	if err != nil {
		return nil, fmt.Errorf("normalize resume input scope %q: %w", scopeName, err)
	}
	return normalized, nil
}

func mergeStateMap(target State, overlay State) {
	if target == nil || overlay == nil {
		return
	}

	if conversation := conversationSource(overlay); conversation != nil {
		copyConversationState(target, conversation)
	}

	for scopeName, scopeState := range overlay.scopes() {
		mergeStateMap(target.EnsureScope(scopeName), scopeState)
	}

	for key, value := range overlay {
		if isInfrastructureStateKey(key) || isSpecialStateKey(key) {
			continue
		}

		if existing, ok := asStateMap(target[key]); ok {
			if next, ok := asStateMap(value); ok {
				mergeStateMap(existing, next)
				target[key] = existing
				continue
			}
		}
		target[key] = cloneStateValue(value)
	}
}

func prepareContinuationState(base State, input State) (State, error) {
	state := State{}
	if base != nil {
		state = base.CloneState()
	}
	resetConversationTurnState(state)
	return mergeResumeInput(state, input)
}

func resetConversationTurnState(state State) {
	if state == nil {
		return
	}

	if conversation := conversationSource(state); conversation != nil {
		delete(conversation, stateKeyFinalAnswer)
		delete(conversation, stateKeyIterationCount)
	}

	for _, scopeState := range state.scopes() {
		resetConversationTurnState(scopeState)
	}
}
