package runtime

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

const (
	stateKeyMessages       = StateKeyMessages
	stateKeyIterationCount = StateKeyIterationCount
	stateKeyMaxIterations  = StateKeyMaxIterations
	stateKeyFinalAnswer    = StateKeyFinalAnswer
)

type ConversationState struct {
	Messages       []StateMessage `json:"messages,omitempty"`
	FinalAnswer    string         `json:"final_answer,omitempty"`
	IterationCount int            `json:"iteration_count,omitempty"`
	MaxIterations  int            `json:"max_iterations,omitempty"`
}

type conversationExtension struct{}

func (conversationExtension) FieldDefinitions() []StateFieldDefinition {
	return []StateFieldDefinition{
		{
			Name:        StateKeyMessages,
			Description: "Chat messages accumulated during the graph run.",
			Schema: map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
				},
			},
		},
		{
			Name:        StateKeyIterationCount,
			Description: "Current tool-using iteration count.",
			Schema:      map[string]any{"type": "integer", "minimum": 0},
		},
		{
			Name:        StateKeyMaxIterations,
			Description: "Maximum iteration count allowed for the run.",
			Schema:      map[string]any{"type": "integer", "minimum": 1},
		},
		{
			Name:        StateKeyFinalAnswer,
			Description: "Final answer produced by the graph.",
			Schema:      map[string]any{"type": "string"},
		},
	}
}

func (conversationExtension) IsSpecialKey(key string) bool {
	switch key {
	case stateKeyMessages, stateKeyIterationCount, stateKeyMaxIterations, stateKeyFinalAnswer:
		return true
	default:
		return false
	}
}

func (conversationExtension) ExtractRootSnapshot(state State, snapshot *StateSnapshot) error {
	if snapshot == nil {
		return nil
	}
	conversation, err := extractConversationState(state)
	if err != nil {
		return err
	}
	snapshot.Conversation = conversation
	return nil
}

func (conversationExtension) ApplyRootSnapshot(state State, snapshot StateSnapshot) error {
	return applyConversationState(state, snapshot.Conversation)
}

func (conversationExtension) EncodeScopedState(values map[string]any, target GraphState) error {
	conversation, err := extractConversationState(values)
	if err != nil {
		return err
	}
	if conversation == nil {
		return nil
	}
	if len(conversation.Messages) > 0 {
		raw, err := json.Marshal(conversation.Messages)
		if err != nil {
			return fmt.Errorf("marshal scope key %q: %w", stateKeyMessages, err)
		}
		target[stateKeyMessages] = raw
	}
	if conversation.FinalAnswer != "" {
		raw, err := json.Marshal(conversation.FinalAnswer)
		if err != nil {
			return fmt.Errorf("marshal scope key %q: %w", stateKeyFinalAnswer, err)
		}
		target[stateKeyFinalAnswer] = raw
	}
	if conversation.IterationCount != 0 {
		raw, err := json.Marshal(conversation.IterationCount)
		if err != nil {
			return fmt.Errorf("marshal scope key %q: %w", stateKeyIterationCount, err)
		}
		target[stateKeyIterationCount] = raw
	}
	if conversation.MaxIterations != 0 {
		raw, err := json.Marshal(conversation.MaxIterations)
		if err != nil {
			return fmt.Errorf("marshal scope key %q: %w", stateKeyMaxIterations, err)
		}
		target[stateKeyMaxIterations] = raw
	}
	return nil
}

func (conversationExtension) DecodeStateField(target State, key string, value any) bool {
	if target == nil {
		return false
	}
	switch key {
	case stateKeyMessages:
		if messages, ok := value.([]llms.MessageContent); ok {
			setConversationMessages(target, messages)
			return true
		}
	case stateKeyFinalAnswer:
		if text, ok := value.(string); ok {
			setConversationString(target, key, text)
			return true
		}
	case stateKeyIterationCount, stateKeyMaxIterations:
		if count, ok := value.(int); ok {
			setConversationInt(target, key, count)
			return true
		}
	}
	return false
}

func (conversationExtension) NormalizeInputStateField(snapshot *StateSnapshot, key string, value any) (bool, error) {
	if snapshot == nil {
		return false, nil
	}
	switch key {
	case stateKeyMessages:
		messages, err := decodeInputMessages(value)
		if err != nil {
			return true, fmt.Errorf("decode messages: %w", err)
		}
		ensureConversationSnapshot(snapshot).Messages = messages
		return true, nil
	case stateKeyFinalAnswer:
		ensureConversationSnapshot(snapshot).FinalAnswer = strings.TrimSpace(asString(value))
		return true, nil
	case stateKeyIterationCount:
		if count, ok := intFromAny(value); ok {
			ensureConversationSnapshot(snapshot).IterationCount = count
		}
		return true, nil
	case stateKeyMaxIterations:
		if count, ok := intFromAny(value); ok {
			ensureConversationSnapshot(snapshot).MaxIterations = count
		}
		return true, nil
	default:
		return false, nil
	}
}

func (conversationExtension) AppendSnapshotFields(snapshot StateSnapshot, result map[string]json.RawMessage) error {
	if snapshot.Conversation == nil {
		return nil
	}
	rawConversation, err := json.Marshal(snapshot.Conversation)
	if err != nil {
		return err
	}
	result["conversation"] = rawConversation
	return nil
}

func ensureConversationSnapshot(snapshot *StateSnapshot) *ConversationState {
	if snapshot.Conversation == nil {
		snapshot.Conversation = &ConversationState{}
	}
	return snapshot.Conversation
}

func extractConversationState(values map[string]any) (*ConversationState, error) {
	values = conversationSource(values)
	if values == nil {
		return nil, nil
	}

	conv := &ConversationState{}
	hasValue := false

	if rawMessages, ok := conversationMessages(values); ok {
		messages, err := serializeMessages(rawMessages)
		if err != nil {
			return nil, err
		}
		conv.Messages = messages
		hasValue = hasValue || len(messages) > 0
	}
	if answer, ok := conversationString(values, stateKeyFinalAnswer); ok && answer != "" {
		conv.FinalAnswer = answer
		hasValue = true
	}
	if iterationCount, ok := conversationInt(values, stateKeyIterationCount); ok && iterationCount != 0 {
		conv.IterationCount = iterationCount
		hasValue = true
	}
	if maxIterations, ok := conversationInt(values, stateKeyMaxIterations); ok && maxIterations != 0 {
		conv.MaxIterations = maxIterations
		hasValue = true
	}

	if !hasValue {
		return nil, nil
	}
	return conv, nil
}

func applyConversationState(target map[string]any, conversation *ConversationState) error {
	if conversation == nil {
		return nil
	}
	if len(conversation.Messages) > 0 {
		messages, err := deserializeMessages(conversation.Messages)
		if err != nil {
			return err
		}
		setConversationMessages(target, messages)
	}
	if conversation.FinalAnswer != "" {
		setConversationString(target, stateKeyFinalAnswer, conversation.FinalAnswer)
	}
	if conversation.IterationCount != 0 {
		setConversationInt(target, stateKeyIterationCount, conversation.IterationCount)
	}
	if conversation.MaxIterations != 0 {
		setConversationInt(target, stateKeyMaxIterations, conversation.MaxIterations)
	}
	return nil
}

func decodeInputMessages(value any) ([]StateMessage, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}

	var messages []StateMessage
	if err := json.Unmarshal(raw, &messages); err == nil && messagesLookValid(messages) {
		for i := range messages {
			messages[i].Role = normalizeStateMessageRole(messages[i].Role)
		}
		return messages, nil
	}

	var simpleMessages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &simpleMessages); err != nil {
		return nil, err
	}

	messages = make([]StateMessage, 0, len(simpleMessages))
	for _, message := range simpleMessages {
		role := normalizeStateMessageRole(message.Role)
		if role == "" {
			role = string(llms.ChatMessageTypeHuman)
		}
		messages = append(messages, StateMessage{
			Role: role,
			Parts: []StateMessagePart{
				{
					Kind: "text",
					Text: message.Content,
				},
			},
		})
	}
	return messages, nil
}

func messagesLookValid(messages []StateMessage) bool {
	if len(messages) == 0 {
		return true
	}
	for _, message := range messages {
		if strings.TrimSpace(message.Role) == "" {
			return false
		}
	}
	return true
}

func normalizeStateMessageRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "", "user", "human":
		return string(llms.ChatMessageTypeHuman)
	case "assistant", "ai":
		return string(llms.ChatMessageTypeAI)
	case "system":
		return string(llms.ChatMessageTypeSystem)
	case "tool":
		return string(llms.ChatMessageTypeTool)
	default:
		return strings.TrimSpace(role)
	}
}

func asString(value any) string {
	text, _ := value.(string)
	return text
}

func intFromAny(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int8:
		return int(typed), true
	case int16:
		return int(typed), true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case float32:
		return int(typed), true
	case float64:
		return int(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return int(parsed), true
	case string:
		if strings.TrimSpace(typed) == "" {
			return 0, false
		}
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}
