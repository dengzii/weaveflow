// Package conversation provides state-bound conversation capabilities.
package conversation

import (
	"errors"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/state"

	"github.com/tmc/langchaingo/llms"
)

const (
	CapabilityID = "weaveflow.conversation.v1"

	FieldMessages       = "messages"
	FieldFinalAnswer    = "final_answer"
	FieldIterationCount = "iteration_count"
	FieldMaxIterations  = "max_iterations"

	DefaultMaxIterations = 8
)

type View struct {
	access            *state.Access
	root              state.Path
	messagesRef       state.Ref[any]
	finalAnswerRef    state.Ref[string]
	iterationCountRef state.Ref[int]
	maxIterationsRef  state.Ref[int]
}

func Definition() dsl.StateCapabilityDefinition {
	return dsl.StateCapabilityDefinition{
		ID:     CapabilityID,
		Schema: dsl.JSONSchema{"type": "object"},
		Fields: []dsl.StateCapabilityFieldDefinition{
			{Name: FieldMessages, Schema: dsl.JSONSchema{"type": "array", "items": dsl.JSONSchema{"type": "object"}}, MergeStrategy: dsl.StateMergeReplace},
			{Name: FieldFinalAnswer, Schema: dsl.JSONSchema{"type": "string"}, MergeStrategy: dsl.StateMergeReplace},
			{Name: FieldIterationCount, Schema: dsl.JSONSchema{"type": "integer", "minimum": 0}, MergeStrategy: dsl.StateMergeReplace},
			{Name: FieldMaxIterations, Schema: dsl.JSONSchema{"type": "integer", "minimum": 1}, MergeStrategy: dsl.StateMergeReplace},
		},
	}
}

func Bind(access *state.Access, root state.Path) (*View, error) {
	if access == nil {
		return nil, errors.New("state access is nil")
	}
	if root.Empty() {
		return nil, errors.New("conversation root path is required")
	}
	return &View{
		access:            access,
		root:              root,
		messagesRef:       state.NewRef[any](root.MustChild(FieldMessages)).WithMerge(state.MergeReplace),
		finalAnswerRef:    state.NewRef[string](root.MustChild(FieldFinalAnswer)).WithMerge(state.MergeReplace),
		iterationCountRef: state.NewRef[int](root.MustChild(FieldIterationCount)).WithMerge(state.MergeReplace),
		maxIterationsRef:  state.NewRef[int](root.MustChild(FieldMaxIterations)).WithMerge(state.MergeReplace),
	}, nil
}

func (v *View) Path() state.Path {
	if v == nil {
		return state.Path{}
	}
	return v.root
}

func (v *View) Messages() []llms.MessageContent {
	messages, _ := v.ReadMessages()
	return messages
}

func (v *View) ReadMessages() ([]llms.MessageContent, error) {
	if v == nil {
		return nil, errors.New("conversation view is nil")
	}
	raw, ok := v.access.ReadAny(v.messagesRef.Path())
	if !ok || raw == nil {
		return nil, nil
	}
	return DecodeMessages(raw)
}

func (v *View) SetMessages(messages []llms.MessageContent) error {
	if v == nil {
		return errors.New("conversation view is nil")
	}
	value, err := EncodeMessages(cloneMessages(messages))
	if err != nil {
		return err
	}
	return state.Replace(v.access, v.messagesRef, any(value))
}

func (v *View) AppendMessage(message llms.MessageContent) error {
	messages, err := v.ReadMessages()
	if err != nil {
		return err
	}
	return v.SetMessages(append(messages, cloneMessage(message)))
}

func (v *View) FinalAnswer() string {
	if v == nil {
		return ""
	}
	value, _ := state.Read(v.access, v.finalAnswerRef)
	return value
}

func (v *View) SetFinalAnswer(answer string) error {
	if v == nil {
		return errors.New("conversation view is nil")
	}
	return state.Replace(v.access, v.finalAnswerRef, answer)
}

func (v *View) IterationCount() int {
	if v == nil {
		return 0
	}
	value, _ := state.Read(v.access, v.iterationCountRef)
	return value
}

func (v *View) SetIterationCount(count int) error {
	if v == nil {
		return errors.New("conversation view is nil")
	}
	if count < 0 {
		count = 0
	}
	return state.Replace(v.access, v.iterationCountRef, count)
}

func (v *View) IncrementIteration() error {
	return v.SetIterationCount(v.IterationCount() + 1)
}

func (v *View) ResetIteration() error {
	return v.SetIterationCount(0)
}

func (v *View) MaxIterations() int {
	if v == nil {
		return DefaultMaxIterations
	}
	value, ok := state.Read(v.access, v.maxIterationsRef)
	if !ok || value <= 0 {
		return DefaultMaxIterations
	}
	return value
}

func (v *View) SetMaxIterations(maxIterations int) error {
	if v == nil {
		return errors.New("conversation view is nil")
	}
	if maxIterations <= 0 {
		maxIterations = DefaultMaxIterations
	}
	return state.Replace(v.access, v.maxIterationsRef, maxIterations)
}

func cloneMessages(messages []llms.MessageContent) []llms.MessageContent {
	if len(messages) == 0 {
		return nil
	}
	cloned := make([]llms.MessageContent, len(messages))
	for index, message := range messages {
		cloned[index] = cloneMessage(message)
	}
	return cloned
}

func cloneMessage(message llms.MessageContent) llms.MessageContent {
	return llms.MessageContent{Role: message.Role, Parts: append([]llms.ContentPart(nil), message.Parts...)}
}
