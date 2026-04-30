package runtime

import "github.com/tmc/langchaingo/llms"

// ConversationFacet is an adapter over State that exposes chat-oriented
// lifecycle fields without making them part of the core State contract.
type ConversationFacet interface {
	Messages() []llms.MessageContent
	IterationCount() int
	MaxIterations() int
	FinalAnswer() string
	SetFinalAnswer(answer string)
	SetMaxIterations(maxIterations int)
	IncrementIteration()
	UpdateMessage(messages []llms.MessageContent)
}

type conversationState struct {
	root  State
	scope string
}

func (s State) Conversation(scope string) ConversationFacet {
	return &conversationState{root: s, scope: scope}
}

func (c *conversationState) Messages() []llms.MessageContent {
	if c == nil {
		return nil
	}
	if messages, ok := conversationMessages(conversationSource(c.targetState(false))); ok {
		return cloneMessages(messages)
	}
	if c.scope != "" {
		return c.root.Conversation("").Messages()
	}
	return nil
}

func (c *conversationState) IterationCount() int {
	if c == nil {
		return 0
	}
	value, _ := conversationInt(conversationSource(c.targetState(false)), stateKeyIterationCount)
	return value
}

func (c *conversationState) MaxIterations() int {
	if c == nil {
		return defaultMaxIterations
	}
	if value, ok := conversationInt(conversationSource(c.targetState(false)), stateKeyMaxIterations); ok && value > 0 {
		return value
	}
	if c.scope != "" {
		return c.root.Conversation("").MaxIterations()
	}
	return defaultMaxIterations
}

func (c *conversationState) FinalAnswer() string {
	if c == nil {
		return ""
	}
	value, _ := conversationString(conversationSource(c.targetState(false)), stateKeyFinalAnswer)
	return value
}

func (c *conversationState) SetFinalAnswer(answer string) {
	if c == nil {
		return
	}
	setConversationString(c.targetState(true), stateKeyFinalAnswer, answer)
}

func (c *conversationState) SetMaxIterations(maxIterations int) {
	if c == nil {
		return
	}
	if maxIterations <= 0 {
		maxIterations = defaultMaxIterations
	}
	setConversationInt(c.targetState(true), stateKeyMaxIterations, maxIterations)
}

func (c *conversationState) IncrementIteration() {
	if c == nil {
		return
	}
	target := c.targetState(true)
	current, _ := conversationInt(conversationSource(target), stateKeyIterationCount)
	setConversationInt(target, stateKeyIterationCount, current+1)
}

func (c *conversationState) UpdateMessage(messages []llms.MessageContent) {
	if c == nil {
		return
	}
	setConversationMessages(c.targetState(true), messages)
}

func (c *conversationState) targetState(create bool) State {
	if c == nil || c.root == nil {
		return nil
	}
	if c.scope == "" {
		return c.root
	}
	if create {
		return c.root.EnsureScope(c.scope)
	}
	return c.root.Scope(c.scope)
}

func conversationSource(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	return namespaceState(values, stateNamespaceConversation, false)
}

func copyConversationState(target State, source map[string]any) {
	if target == nil || source == nil {
		return
	}
	if messages, ok := conversationMessages(source); ok {
		setConversationMessages(target, messages)
	}
	if iterationCount, ok := conversationInt(source, stateKeyIterationCount); ok {
		setConversationInt(target, stateKeyIterationCount, iterationCount)
	}
	if maxIterations, ok := conversationInt(source, stateKeyMaxIterations); ok {
		setConversationInt(target, stateKeyMaxIterations, maxIterations)
	}
	if answer, ok := conversationString(source, stateKeyFinalAnswer); ok {
		setConversationString(target, stateKeyFinalAnswer, answer)
	}
}

func conversationMessages(values map[string]any) ([]llms.MessageContent, bool) {
	if values == nil {
		return nil, false
	}
	typed, ok := values[stateKeyMessages].([]llms.MessageContent)
	return typed, ok
}

func conversationInt(values map[string]any, key string) (int, bool) {
	if values == nil {
		return 0, false
	}
	typed, ok := values[key].(int)
	return typed, ok
}

func conversationString(values map[string]any, key string) (string, bool) {
	if values == nil {
		return "", false
	}
	typed, ok := values[key].(string)
	return typed, ok
}

func setConversationMessages(values map[string]any, messages []llms.MessageContent) {
	if values == nil {
		return
	}
	cloned := cloneMessages(messages)
	namespaceState(values, stateNamespaceConversation, true)[stateKeyMessages] = cloned
}

func setConversationInt(values map[string]any, key string, value int) {
	if values == nil {
		return
	}
	namespaceState(values, stateNamespaceConversation, true)[key] = value
}

func setConversationString(values map[string]any, key string, value string) {
	if values == nil {
		return
	}
	namespaceState(values, stateNamespaceConversation, true)[key] = value
}
