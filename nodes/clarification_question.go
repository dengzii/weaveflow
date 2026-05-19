package nodes

import (
	"context"
	"strings"
	"weaveflow/dsl"
	fruntime "weaveflow/runtime"
	wfstate "weaveflow/state"

	"github.com/google/uuid"
	langgraph "github.com/smallnest/langgraphgo/graph"
)

const (
	ClarificationStateKey            = "clarification"
	ClarificationUserChoiceKey       = "user_choice"
	clarificationEventKind           = "clarification_question"
	clarificationMaxAttempts         = 2
	clarificationDefaultInterruptMsg = "interrupt due to waiting for clarification from user"
)

type ClarificationQuestionNode struct {
	NodeInfo
	StateScope       string
	InterruptMessage string
	MaxAttempts      int
}

func NewClarificationQuestionNode() *ClarificationQuestionNode {
	id := uuid.New()
	return &ClarificationQuestionNode{
		NodeInfo: NodeInfo{
			NodeID:          "Clarification_" + id.String(),
			NodeName:        "ClarificationQuestion",
			NodeDescription: "Pause the graph until the user disambiguates the request, then route back to the router.",
		},
		InterruptMessage: clarificationDefaultInterruptMsg,
		MaxAttempts:      clarificationMaxAttempts,
	}
}

func (n *ClarificationQuestionNode) execute(ctx context.Context, state wfstate.State) (wfstate.State, error) {
	if state == nil {
		state = wfstate.State{}
	}

	orchestration := state.Ensure(wfstate.StateKeyOrchestration)

	if choice, ok := n.consumeUserChoice(state); ok {
		return n.applyUserChoice(ctx, state, orchestration, choice)
	}

	attempts := clarificationAttempts(orchestration)
	if attempts >= n.effectiveMaxAttempts() {
		orchestration["clarification_exhausted"] = true
		_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, map[string]any{
			"kind":     clarificationEventKind,
			"phase":    "exhausted",
			"attempts": attempts,
		})
		return state, nil
	}

	question := strings.TrimSpace(stringFromMap(orchestration, "clarification_question"))
	options := clarificationOptions(orchestration)
	reasoning := strings.TrimSpace(stringFromMap(orchestration, "reasoning"))

	payload := map[string]any{
		"kind":      clarificationEventKind,
		"phase":     "pending",
		"question":  question,
		"options":   options,
		"reasoning": reasoning,
		"attempts":  attempts,
	}
	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "clarification.pending", payload)
	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, payload)

	return state, &langgraph.NodeInterrupt{Node: n.NodeID, Value: n.effectiveInterruptMessage(question)}
}

func (n *ClarificationQuestionNode) applyUserChoice(ctx context.Context, state wfstate.State, orchestration wfstate.State, choice string) (wfstate.State, error) {
	originalRequest := ""
	if request := state.Get(wfstate.StateKeyRequest); request != nil {
		originalRequest, _ = request["input"].(string)
	}
	originalQuestion := strings.TrimSpace(stringFromMap(orchestration, "clarification_question"))

	rewritten := buildClarifiedInput(originalRequest, originalQuestion, choice)

	request := state.Ensure(wfstate.StateKeyRequest)
	request["input"] = rewritten

	planner := state.Ensure(wfstate.StateKeyPlanner)
	planner["objective"] = rewritten

	attempts := clarificationAttempts(orchestration) + 1
	orchestration["needs_clarification"] = false
	orchestration["clarification_question"] = ""
	orchestration["clarification_options"] = nil
	orchestration["clarification_attempts"] = attempts
	orchestration["last_clarification_choice"] = choice
	if attempts >= n.effectiveMaxAttempts() {
		orchestration["clarification_exhausted"] = true
	}

	payload := map[string]any{
		"kind":     clarificationEventKind,
		"phase":    "resolved",
		"choice":   choice,
		"attempts": attempts,
	}
	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "clarification.resolved", payload)
	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, payload)

	return state, nil
}

func (n *ClarificationQuestionNode) consumeUserChoice(state wfstate.State) (string, bool) {
	target := state.Get(ClarificationStateKey)
	if target == nil {
		return "", false
	}
	raw, exists := target[ClarificationUserChoiceKey]
	if !exists {
		return "", false
	}
	delete(target, ClarificationUserChoiceKey)
	text, ok := raw.(string)
	if !ok {
		return "", false
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	return text, true
}

func (n *ClarificationQuestionNode) effectiveMaxAttempts() int {
	if n == nil || n.MaxAttempts <= 0 {
		return clarificationMaxAttempts
	}
	return n.MaxAttempts
}

func (n *ClarificationQuestionNode) effectiveInterruptMessage(question string) string {
	message := strings.TrimSpace(n.InterruptMessage)
	if message == "" {
		message = clarificationDefaultInterruptMsg
	}
	if question != "" {
		return message + "\n" + question
	}
	return message
}

func (n *ClarificationQuestionNode) Execute(ctx context.Context, input wfstate.State) (wfstate.StatePatch, error) {
	return executeStatePatch(input, func(state wfstate.State) (wfstate.State, error) {
		return n.execute(ctx, state)
	})
}

func (n *ClarificationQuestionNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return dsl.GraphNodeSpec{
		ID:          n.ID(),
		Name:        n.Name(),
		Type:        "clarification_question",
		Description: n.Description(),
		Config: map[string]any{
			"state_scope":       n.StateScope,
			"interrupt_message": n.InterruptMessage,
			"max_attempts":      n.MaxAttempts,
		},
	}
}

func clarificationAttempts(orchestration wfstate.State) int {
	if orchestration == nil {
		return 0
	}
	switch typed := orchestration["clarification_attempts"].(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	}
	return 0
}

func clarificationOptions(orchestration wfstate.State) []string {
	if orchestration == nil {
		return nil
	}
	switch typed := orchestration["clarification_options"].(type) {
	case []string:
		out := make([]string, 0, len(typed))
		for _, option := range typed {
			option = strings.TrimSpace(option)
			if option != "" {
				out = append(out, option)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(typed))
		for _, raw := range typed {
			if text, ok := raw.(string); ok {
				text = strings.TrimSpace(text)
				if text != "" {
					out = append(out, text)
				}
			}
		}
		return out
	}
	return nil
}

func buildClarifiedInput(originalRequest string, originalQuestion string, choice string) string {
	originalRequest = strings.TrimSpace(originalRequest)
	originalQuestion = strings.TrimSpace(originalQuestion)
	choice = strings.TrimSpace(choice)

	var b strings.Builder
	if originalRequest != "" {
		b.WriteString(originalRequest)
	}
	if originalQuestion != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("Clarifying question: ")
		b.WriteString(originalQuestion)
	}
	if choice != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("User clarified: ")
		b.WriteString(choice)
	}
	return b.String()
}
