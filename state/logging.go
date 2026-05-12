package state

import "go.uber.org/zap"

func SummaryFields(s State) []zap.Field {
	return []zap.Field{
		zap.Int("state_keys", CountKeys(s)),
		zap.Int("state_scopes", len(s.Scopes())),
		zap.Int("conversation_messages", CountConversationMessages(s)),
	}
}

func CountKeys(s State) int {
	if s == nil {
		return 0
	}

	count := 0
	for key := range s {
		if isInfrastructureStateKey(key) || isSpecialStateKey(key) || isInternalSnapshotNamespaceKey(key) {
			continue
		}
		count++
	}
	return count
}

func CountConversationMessages(s State) int {
	if s == nil {
		return 0
	}

	total := len(s.Conversation("").Messages())
	for _, scopeState := range s.Scopes() {
		total += len(scopeState.Conversation("").Messages())
	}
	return total
}
