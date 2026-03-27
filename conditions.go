package falcon

import (
	"context"

	"github.com/tmc/langchaingo/llms"
)

func LastMessageHasToolCalls(scope string) EdgeCondition {
	return func(_ context.Context, state State) bool {
		messages := scopedState(state, scope).GetMessages()
		if len(messages) == 0 {
			return false
		}

		lastMessage := messages[len(messages)-1]
		if lastMessage.Role != llms.ChatMessageTypeAI {
			return false
		}

		for _, part := range lastMessage.Parts {
			if _, ok := part.(llms.ToolCall); ok {
				return true
			}
		}

		return false
	}
}

func HasFinalAnswer(scope string) EdgeCondition {
	return func(_ context.Context, state State) bool {
		return scopedState(state, scope).FinalAnswer() != ""
	}
}

func ExpressConditions() EdgeCondition {
	return func(_ context.Context, state State) bool {
		_ = state
		return false
	}
}
