package falcon

import (
	"context"
	fruntime "falcon/runtime"
	"fmt"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

type EdgeConditionMatcher func(ctx context.Context, state State) bool

// EdgeCondition is a condition that can be applied to an edge in a graph.
type EdgeCondition struct {
	Spec  GraphConditionSpec
	Match EdgeConditionMatcher
}

func NewEdgeCondition(spec GraphConditionSpec, match EdgeConditionMatcher) EdgeCondition {
	return EdgeCondition{
		Spec:  normalizeGraphConditionSpec(spec),
		Match: match,
	}
}

func (c EdgeCondition) validate() error {
	spec := normalizeGraphConditionSpec(c.Spec)
	if spec.Type == "" {
		return fmt.Errorf("condition spec type is required")
	}
	if c.Match == nil {
		return fmt.Errorf("condition matcher is nil")
	}
	return nil
}

func (c EdgeCondition) withSpec(spec GraphConditionSpec) EdgeCondition {
	c.Spec = normalizeGraphConditionSpec(spec)
	return c
}

func (c EdgeCondition) cloneSpec() GraphConditionSpec {
	spec := normalizeGraphConditionSpec(c.Spec)
	if len(spec.Config) > 0 {
		spec.Config = cloneMap(spec.Config)
	}
	return spec
}

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
