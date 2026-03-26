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
