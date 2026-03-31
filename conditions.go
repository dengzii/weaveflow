package falcon

import (
	"context"
	fruntime "falcon/runtime"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

func LastMessageHasToolCalls(scope string) EdgeCondition {
	scope = strings.TrimSpace(scope)
	spec := GraphConditionSpec{Type: "last_message_has_tool_calls"}
	if scope != "" {
		spec.Config = map[string]any{
			"state_scope": scope,
		}
	}
	return NewEdgeCondition(spec, func(_ context.Context, state State) bool {
		messages := fruntime.Conversation(state, scope).Messages()
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
	})
}

func HasFinalAnswer(scope string) EdgeCondition {
	scope = strings.TrimSpace(scope)
	spec := GraphConditionSpec{Type: "has_final_answer"}
	if scope != "" {
		spec.Config = map[string]any{
			"state_scope": scope,
		}
	}
	return NewEdgeCondition(spec, func(_ context.Context, state State) bool {
		return fruntime.Conversation(state, scope).FinalAnswer() != ""
	})
}

func ExpressConditions() EdgeCondition {
	return NewEdgeCondition(GraphConditionSpec{Type: "express_conditions"}, func(_ context.Context, state State) bool {
		_ = state
		return false
	})
}
