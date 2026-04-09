package nodes

import (
	"context"
	"fmt"
	"strings"
	"weaveflow/dsl"
	fruntime "weaveflow/runtime"

	"github.com/google/uuid"
	langgraph "github.com/smallnest/langgraphgo/graph"
	"github.com/tmc/langchaingo/llms"
)

type HumanMessageNode struct {
	NodeInfo
	StateScope       string
	InterruptMessage string
}

const (
	PendingHumanInputStateKey = "pending_human_input"
)

func NewHumanMessageNode() *HumanMessageNode {
	id := uuid.New()
	return &HumanMessageNode{
		NodeInfo: NodeInfo{
			NodeID:          "HumanMessage_" + id.String(),
			NodeName:        "HumanMessage",
			NodeDescription: "Pause the graph until a human message is provided.",
		},
		InterruptMessage: "interrupt due to waiting a human message",
		StateScope:       "",
	}
}

func (n *HumanMessageNode) Invoke(ctx context.Context, state fruntime.State) (fruntime.State, error) {
	conversation := fruntime.Conversation(state, n.StateScope)
	pending, ok, err := n.consumePendingInput(state)
	if err != nil {
		return state, err
	}
	if ok {
		messages := conversation.Messages()
		conversation.UpdateMessage(append(messages, llms.TextParts(llms.ChatMessageTypeHuman, pending)))
		return state, nil
	}

	messages := conversation.Messages()
	if len(messages) <= 0 {
		return state, nil
	}

	lastMessage := messages[len(messages)-1]
	if lastMessage.Role != llms.ChatMessageTypeHuman {
		return state, &langgraph.NodeInterrupt{Node: n.NodeID, Value: n.InterruptMessage}
	}

	return state, nil
}

func (n *HumanMessageNode) consumePendingInput(state fruntime.State) (string, bool, error) {
	target := n.pendingInputState(state)
	if target == nil {
		return "", false, nil
	}

	raw, exists := target[PendingHumanInputStateKey]
	if !exists {
		return "", false, nil
	}
	delete(target, PendingHumanInputStateKey)
	if raw == nil {
		return "", false, nil
	}

	text, ok := raw.(string)
	if !ok {
		return "", false, fmt.Errorf("state scope %q field %q must be string, got %T", n.StateScope, PendingHumanInputStateKey, raw)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false, nil
	}
	return text, true, nil
}

func (n *HumanMessageNode) pendingInputState(state fruntime.State) fruntime.State {
	if state == nil {
		return nil
	}
	if n.StateScope == "" {
		return state
	}
	return state.Scope(n.StateScope)
}

func (n *HumanMessageNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return dsl.GraphNodeSpec{
		ID:          n.ID(),
		Name:        n.Name(),
		Type:        "human_message",
		Description: n.Description(),
		Config: map[string]any{
			"state_scope":       n.StateScope,
			"interrupt_message": n.InterruptMessage,
		},
	}
}
