package runtime

import (
	"strings"

	"github.com/tmc/langchaingo/llms"
)

const (
	StateKeyMessages       = "messages"
	StateKeyIterationCount = "iteration_count"
	StateKeyMaxIterations  = "max_iterations"
	StateKeyFinalAnswer    = "final_answer"

	StateNamespacePrefix       = "__falcon_"
	stateNamespaceConversation = "__falcon_conversation"
	stateNamespaceScopes       = "__falcon_scopes"

	DefaultMaxIterations = 8
)

const (
	stateKeyMessages       = StateKeyMessages
	stateKeyIterationCount = StateKeyIterationCount
	stateKeyMaxIterations  = StateKeyMaxIterations
	stateKeyFinalAnswer    = StateKeyFinalAnswer

	stateNamespacePrefix = StateNamespacePrefix

	defaultMaxIterations = DefaultMaxIterations
)

// State stores shared business data at the root level.
// Falcon-managed scope and conversation state live under reserved namespaces.
type State map[string]any

func NewBaseState(messages []llms.MessageContent, maxIterations int) State {
	state := State{}
	conversation := Conversation(state, "")
	conversation.UpdateMessage(messages)
	conversation.SetMaxIterations(maxIterations)
	return state
}

func (s State) CloneState() State {
	if s == nil {
		return nil
	}

	cloned := State{}
	scopeNames := s.scopeNames()
	for key, value := range s {
		if isInfrastructureStateKey(key) || isConversationKey(key) {
			continue
		}
		if _, ok := scopeNames[key]; ok {
			continue
		}
		cloned[key] = cloneStateValue(value)
	}

	if conversation := conversationSource(s); conversation != nil {
		copyConversationState(cloned, conversation)
	}

	for scopeName, scopeState := range s.scopes() {
		setScopeState(cloned, scopeName, cloneStateMap(scopeState))
	}

	return cloned
}

func (s State) Scope(scope string) State {
	return s.scopeState(scope, false)
}

func (s State) EnsureScope(scope string) State {
	return s.scopeState(scope, true)
}

func (s State) scopeState(scope string, create bool) State {
	if s == nil || scope == "" {
		return nil
	}

	if scopes := s.scopesNamespace(false); scopes != nil {
		if scopeState, ok := asStateMap(scopes[scope]); ok {
			return scopeState
		}
	}
	if !create {
		return nil
	}
	scopeState := State{}
	setScopeState(s, scope, scopeState)
	return scopeState
}

func (s State) Namespace(namespace string) State {
	return namespaceState(s, namespace, false)
}

func (s State) EnsureNamespace(namespace string) State {
	return namespaceState(s, namespace, true)
}

func namespaceState(values map[string]any, namespace string, create bool) State {
	if values == nil || namespace == "" {
		return nil
	}
	key := normalizeStateNamespace(namespace)
	switch typed := values[key].(type) {
	case State:
		return typed
	case map[string]any:
		nested := State(typed)
		values[key] = nested
		return nested
	}
	if !create {
		return nil
	}
	nested := State{}
	values[key] = nested
	return nested
}

func (s State) scopesNamespace(create bool) State {
	return namespaceState(s, stateNamespaceScopes, create)
}

func (s State) scopes() map[string]State {
	rawScopes := s.scopesNamespace(false)
	if rawScopes == nil {
		return nil
	}

	scopes := make(map[string]State, len(rawScopes))
	for scopeName, rawState := range rawScopes {
		if scopeState, ok := asStateMap(rawState); ok {
			scopes[scopeName] = scopeState
		}
	}
	if len(scopes) == 0 {
		return nil
	}
	return scopes
}

func (s State) scopeNames() map[string]struct{} {
	scopes := s.scopes()
	if len(scopes) == 0 {
		return nil
	}

	names := make(map[string]struct{}, len(scopes))
	for scopeName := range scopes {
		names[scopeName] = struct{}{}
	}
	return names
}

func setScopeState(root State, scope string, scopeState State) {
	if root == nil || scope == "" {
		return
	}
	scopes := root.scopesNamespace(true)
	scopes[scope] = scopeState
}

func isInternalStateKey(key string) bool {
	return strings.HasPrefix(key, stateNamespacePrefix)
}

func isInfrastructureStateKey(key string) bool {
	switch key {
	case stateNamespaceConversation, stateNamespaceScopes:
		return true
	default:
		return false
	}
}

func NormalizeStateNamespace(namespace string) string {
	if strings.HasPrefix(namespace, stateNamespacePrefix) {
		return namespace
	}
	return stateNamespacePrefix + namespace
}

func normalizeStateNamespace(namespace string) string {
	return NormalizeStateNamespace(namespace)
}

func asStateMap(value any) (State, bool) {
	switch typed := value.(type) {
	case State:
		return typed, true
	case map[string]any:
		return State(typed), true
	default:
		return nil, false
	}
}

func cloneStateMap(input map[string]any) State {
	if input == nil {
		return nil
	}

	cloned := make(State, len(input))
	for key, value := range input {
		cloned[key] = cloneStateValue(value)
	}
	return cloned
}

func cloneStateValue(value any) any {
	switch typed := value.(type) {
	case []llms.MessageContent:
		return cloneMessages(typed)
	case []string:
		return cloneStrings(typed)
	case []map[string]any:
		return cloneMapSlice(typed)
	case []any:
		return cloneAnySlice(typed)
	case map[string]any:
		return map[string]any(cloneStateMap(typed))
	case State:
		return State(cloneStateMap(typed))
	default:
		return value
	}
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}

func cloneMapSlice(values []map[string]any) []map[string]any {
	if len(values) == 0 {
		return nil
	}

	cloned := make([]map[string]any, len(values))
	for i, value := range values {
		cloned[i] = map[string]any(cloneStateMap(value))
	}
	return cloned
}

func cloneAnySlice(values []any) []any {
	if len(values) == 0 {
		return nil
	}

	cloned := make([]any, len(values))
	for i, value := range values {
		cloned[i] = cloneStateValue(value)
	}
	return cloned
}

func cloneMessages(messages []llms.MessageContent) []llms.MessageContent {
	if len(messages) == 0 {
		return nil
	}

	cloned := make([]llms.MessageContent, len(messages))
	for i, message := range messages {
		cloned[i] = llms.MessageContent{
			Role:  message.Role,
			Parts: append([]llms.ContentPart(nil), message.Parts...),
		}
	}
	return cloned
}
