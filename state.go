package falcon

import "github.com/tmc/langchaingo/llms"

const (
	stateKeyMessages       = "messages"
	stateKeyIterationCount = "iteration_count"
	stateKeyMaxIterations  = "max_iterations"
	stateKeyFinalAnswer    = "final_answer"
)

type BaseState interface {
	GetMessages() []llms.MessageContent
	IterationCount() int
	MaxIterations() int
	FinalAnswer() string
	SetFinalAnswer(answer string)
	IncrementIteration()
	UpdateMessage(messages []llms.MessageContent)
}

type scopeBinder interface {
	WithScope(scope string) BaseState
}

// State is a dynamic runtime state bag backed by map[string]any.
// Node-local state is represented as nested maps inside this global state.
type State map[string]any

type ScopedState struct {
	root  State
	scope string
}

func NewBaseState(messages []llms.MessageContent, maxIterations int) State {
	state := State{}
	state.UpdateMessage(messages)
	state.SetMaxIterations(maxIterations)
	return state
}

func (s State) CloneState() State {
	if s == nil {
		return nil
	}
	return cloneStateMap(s)
}

func (s State) WithScope(scope string) BaseState {
	if scope == "" {
		return s
	}
	return &ScopedState{root: s, scope: scope}
}

func (s State) GetMessages() []llms.MessageContent {
	if s == nil {
		return nil
	}
	if typed, ok := s[stateKeyMessages].([]llms.MessageContent); ok {
		return cloneMessages(typed)
	}
	return nil
}

func (s State) IterationCount() int {
	if s == nil {
		return 0
	}
	if typed, ok := s[stateKeyIterationCount].(int); ok {
		return typed
	}
	return 0
}

func (s State) MaxIterations() int {
	if s == nil {
		return 8
	}
	if typed, ok := s[stateKeyMaxIterations].(int); ok && typed > 0 {
		return typed
	}
	return 8
}

func (s State) SetMaxIterations(maxIterations int) {
	if s == nil {
		return
	}
	if maxIterations <= 0 {
		s[stateKeyMaxIterations] = 8
		return
	}
	s[stateKeyMaxIterations] = maxIterations
}

func (s State) FinalAnswer() string {
	if s == nil {
		return ""
	}
	if typed, ok := s[stateKeyFinalAnswer].(string); ok {
		return typed
	}
	return ""
}

func (s State) SetFinalAnswer(answer string) {
	if s == nil {
		return
	}
	s[stateKeyFinalAnswer] = answer
}

func (s State) IncrementIteration() {
	if s == nil {
		return
	}
	s[stateKeyIterationCount] = s.IterationCount() + 1
}

func (s State) UpdateMessage(messages []llms.MessageContent) {
	if s == nil {
		return
	}
	s[stateKeyMessages] = cloneMessages(messages)
}

func (s *ScopedState) WithScope(scope string) BaseState {
	if scope == "" {
		return s
	}
	return &ScopedState{root: s.root, scope: scope}
}

func (s *ScopedState) GetMessages() []llms.MessageContent {
	if s == nil {
		return nil
	}
	scopeState := s.scopeState(false)
	if scopeState != nil {
		if typed, ok := scopeState[stateKeyMessages].([]llms.MessageContent); ok {
			return cloneMessages(typed)
		}
	}
	if s.root == nil {
		return nil
	}
	if typed, ok := s.root[stateKeyMessages].([]llms.MessageContent); ok {
		return cloneMessages(typed)
	}
	return nil
}

func (s *ScopedState) IterationCount() int {
	if s == nil {
		return 0
	}
	scopeState := s.scopeState(false)
	if scopeState == nil {
		return 0
	}
	if typed, ok := scopeState[stateKeyIterationCount].(int); ok {
		return typed
	}
	return 0
}

func (s *ScopedState) MaxIterations() int {
	if s == nil {
		return 8
	}
	scopeState := s.scopeState(false)
	if scopeState != nil {
		if typed, ok := scopeState[stateKeyMaxIterations].(int); ok && typed > 0 {
			return typed
		}
	}
	if s.root != nil {
		if typed, ok := s.root[stateKeyMaxIterations].(int); ok && typed > 0 {
			return typed
		}
	}
	return 8
}

func (s *ScopedState) SetMaxIterations(maxIterations int) {
	if s == nil {
		return
	}
	scopeState := s.scopeState(true)
	if maxIterations <= 0 {
		scopeState[stateKeyMaxIterations] = 8
		return
	}
	scopeState[stateKeyMaxIterations] = maxIterations
}

func (s *ScopedState) FinalAnswer() string {
	if s == nil {
		return ""
	}
	scopeState := s.scopeState(false)
	if scopeState == nil {
		return ""
	}
	if typed, ok := scopeState[stateKeyFinalAnswer].(string); ok {
		return typed
	}
	return ""
}

func (s *ScopedState) SetFinalAnswer(answer string) {
	if s == nil {
		return
	}
	s.scopeState(true)[stateKeyFinalAnswer] = answer
}

func (s *ScopedState) IncrementIteration() {
	if s == nil {
		return
	}
	scopeState := s.scopeState(true)
	current, _ := scopeState[stateKeyIterationCount].(int)
	scopeState[stateKeyIterationCount] = current + 1
}

func (s *ScopedState) UpdateMessage(messages []llms.MessageContent) {
	if s == nil {
		return
	}
	s.scopeState(true)[stateKeyMessages] = cloneMessages(messages)
}

func (s *ScopedState) scopeState(create bool) map[string]any {
	if s == nil || s.root == nil || s.scope == "" {
		return nil
	}
	switch typed := s.root[s.scope].(type) {
	case map[string]any:
		return typed
	case State:
		return typed
	}
	if !create {
		return nil
	}
	scopeState := map[string]any{}
	s.root[s.scope] = scopeState
	return scopeState
}

func scopedState(state BaseState, scope string) BaseState {
	if scope == "" || state == nil {
		return state
	}
	if binder, ok := state.(scopeBinder); ok {
		return binder.WithScope(scope)
	}
	return state
}

func cloneStateMap(input map[string]any) State {
	cloned := make(State, len(input))
	for key, value := range input {
		switch typed := value.(type) {
		case []llms.MessageContent:
			cloned[key] = cloneMessages(typed)
		case []string:
			cloned[key] = cloneStrings(typed)
		case map[string]any:
			cloned[key] = map[string]any(cloneStateMap(typed))
		case State:
			cloned[key] = map[string]any(cloneStateMap(typed))
		default:
			cloned[key] = value
		}
	}
	return cloned
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
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
