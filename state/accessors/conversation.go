package accessors

import (
	"weaveflow/state"

	"github.com/tmc/langchaingo/llms"
)

const (
	KeyConversation = "conversation"

	ConversationFieldMessages       = "messages"
	ConversationFieldFinalAnswer    = "final_answer"
	ConversationFieldIterationCount = "iteration_count"
	ConversationFieldMaxIterations  = "max_iterations"

	DefaultMaxIterations = 8
)

type Conversation interface {
	Path() state.Path
	Messages() []llms.MessageContent
	SetMessages(messages []llms.MessageContent) error
	AppendMessage(message llms.MessageContent) error
	FinalAnswer() string
	SetFinalAnswer(answer string) error
	IterationCount() int
	SetIterationCount(count int) error
	IncrementIteration() error
	ResetIteration() error
	MaxIterations() int
	SetMaxIterations(maxIterations int) error
}

type conversationAccessor struct {
	access            *state.Access
	path              state.Path
	messagesRef       state.Ref[[]llms.MessageContent]
	finalAnswerRef    state.Ref[string]
	iterationCountRef state.Ref[int]
	maxIterationsRef  state.Ref[int]
}

func (c conversationAccessor) Path() state.Path {
	return c.path
}

func (c conversationAccessor) Messages() []llms.MessageContent {
	messages, _ := state.Read(c.access, c.messagesRef)
	return cloneMessages(messages)
}

func (c conversationAccessor) SetMessages(messages []llms.MessageContent) error {
	return state.Set(c.access, c.messagesRef, cloneMessages(messages))
}

func (c conversationAccessor) AppendMessage(message llms.MessageContent) error {
	messages := append(c.Messages(), cloneMessage(message))
	return c.SetMessages(messages)
}

func (c conversationAccessor) FinalAnswer() string {
	value, _ := state.Read(c.access, c.finalAnswerRef)
	return value
}

func (c conversationAccessor) SetFinalAnswer(answer string) error {
	return state.Set(c.access, c.finalAnswerRef, answer)
}

func (c conversationAccessor) IterationCount() int {
	value, _ := state.Read(c.access, c.iterationCountRef)
	return value
}

func (c conversationAccessor) SetIterationCount(count int) error {
	if count < 0 {
		count = 0
	}
	return state.Set(c.access, c.iterationCountRef, count)
}

func (c conversationAccessor) IncrementIteration() error {
	return c.SetIterationCount(c.IterationCount() + 1)
}

func (c conversationAccessor) ResetIteration() error {
	return c.SetIterationCount(0)
}

func (c conversationAccessor) MaxIterations() int {
	value, ok := state.Read(c.access, c.maxIterationsRef)
	if !ok || value <= 0 {
		return DefaultMaxIterations
	}
	return value
}

func (c conversationAccessor) SetMaxIterations(maxIterations int) error {
	if maxIterations <= 0 {
		maxIterations = DefaultMaxIterations
	}
	return state.Set(c.access, c.maxIterationsRef, maxIterations)
}

func registerConversation(registry *state.Registry) error {
	return registry.RegisterAccessor(state.AccessorDefinition{
		Name: ConversationID.Name(),
		ContractFactory: func(scope string) state.Contract {
			refs := conversationRefs(scope)
			return state.NewContract(
				refs.messages.ReadWriteField(),
				refs.finalAnswer.ReadWriteField(),
				refs.iterationCount.ReadWriteField(),
				refs.maxIterations.ReadWriteField(),
			)
		},
		Factory: func(access *state.Access) any {
			refs := conversationRefs(access.Scope())
			return conversationAccessor{
				access:            access,
				path:              conversationPath(access.Scope()),
				messagesRef:       refs.messages,
				finalAnswerRef:    refs.finalAnswer,
				iterationCountRef: refs.iterationCount,
				maxIterationsRef:  refs.maxIterations,
			}
		},
	})
}

type conversationRefSet struct {
	messages       state.Ref[[]llms.MessageContent]
	finalAnswer    state.Ref[string]
	iterationCount state.Ref[int]
	maxIterations  state.Ref[int]
}

func conversationRefs(scope string) conversationRefSet {
	base := conversationPath(scope)
	return conversationRefSet{
		messages:       state.NewRef[[]llms.MessageContent](base.MustChild(ConversationFieldMessages)).WithMerge(state.MergeReplace),
		finalAnswer:    state.NewRef[string](base.MustChild(ConversationFieldFinalAnswer)),
		iterationCount: state.NewRef[int](base.MustChild(ConversationFieldIterationCount)),
		maxIterations:  state.NewRef[int](base.MustChild(ConversationFieldMaxIterations)),
	}
}

func conversationPath(scope string) state.Path {
	if scope == "" {
		return state.Shared(KeyConversation)
	}
	return state.Scope(scope, KeyConversation)
}

func cloneMessages(messages []llms.MessageContent) []llms.MessageContent {
	if len(messages) == 0 {
		return nil
	}
	cloned := make([]llms.MessageContent, len(messages))
	for i, message := range messages {
		cloned[i] = cloneMessage(message)
	}
	return cloned
}

func cloneMessage(message llms.MessageContent) llms.MessageContent {
	return llms.MessageContent{
		Role:  message.Role,
		Parts: append([]llms.ContentPart(nil), message.Parts...),
	}
}
