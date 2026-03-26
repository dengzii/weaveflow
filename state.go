package falcon

import "github.com/tmc/langchaingo/llms"

type BaseState interface {
	EnabledTools() []string
	GetMessages() []llms.MessageContent
	IterationCount() int
	MaxIterations() int
	FinalAnswer() string
	SetFinalAnswer(answer string)
	IncrementIteration()
	UpdateMessage(messages []llms.MessageContent)
}

type State struct {
	enabledTools   []string
	messages       []llms.MessageContent
	iterationCount int
	maxIterations  int
	finalAnswer    string
}

func NewBaseState(messages []llms.MessageContent, enabledTools []string, maxIterations int) *State {
	return &State{
		enabledTools: cloneStrings(enabledTools),
		messages:     cloneMessages(messages),
		maxIterations: func() int {
			if maxIterations > 0 {
				return maxIterations
			}
			return 8
		}(),
	}
}

func (s *State) EnabledTools() []string {
	return cloneStrings(s.enabledTools)
}

func (s *State) SetEnabledTools(enabledTools []string) {
	s.enabledTools = cloneStrings(enabledTools)
}

func (s *State) GetMessages() []llms.MessageContent {
	return cloneMessages(s.messages)
}

func (s *State) IterationCount() int {
	return s.iterationCount
}

func (s *State) MaxIterations() int {
	return s.maxIterations
}

func (s *State) SetMaxIterations(maxIterations int) {
	if maxIterations <= 0 {
		s.maxIterations = 8
		return
	}
	s.maxIterations = maxIterations
}

func (s *State) FinalAnswer() string {
	return s.finalAnswer
}

func (s *State) SetFinalAnswer(answer string) {
	s.finalAnswer = answer
}

func (s *State) IncrementIteration() {
	s.iterationCount++
}

func (s *State) UpdateMessage(messages []llms.MessageContent) {
	s.messages = cloneMessages(messages)
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
