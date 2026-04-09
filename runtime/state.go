package runtime

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

const (
	StateNamespacePrefix       = "__wf_"
	stateNamespaceConversation = "__wf_conversation"
	stateNamespaceScopes       = "__wf_scopes"

	DefaultMaxIterations = 8
)

const (
	stateNamespacePrefix = StateNamespacePrefix

	defaultMaxIterations = DefaultMaxIterations
)

// State stores shared business data at the root level.
// WeaveFlow-managed scope and conversation state live under reserved namespaces.
//
// Persisted state is intentionally constrained to:
// - primitives
// - map[string]any / State
// - []any
// - []string
// - []map[string]any
// - []llms.MessageContent for conversation messages
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
	for key, value := range s {
		if isInfrastructureStateKey(key) || isSpecialStateKey(key) {
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

func (s State) PrettyString() string {
	bs, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	return string(bs)
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

func setScopeState(root State, scope string, scopeState State) {
	if root == nil || scope == "" {
		return
	}
	scopes := root.scopesNamespace(true)
	scopes[scope] = scopeState
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
		return typed, true
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
		return cloneStateMap(typed)
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
		cloned[i] = cloneStateMap(value)
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
