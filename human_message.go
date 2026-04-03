package falcon

import (
	"context"
	fruntime "falcon/runtime"

	"github.com/google/uuid"
	langgraph "github.com/smallnest/langgraphgo/graph"
	"github.com/tmc/langchaingo/llms"
)

type HumanMessageNode struct {
	NodeInfo
	StateScope       string
	InterruptMessage string
}

const scopeNodeHumanMessage = "node_human_message"

func NewHumanMessageNode() *HumanMessageNode {
	id := uuid.New()
	return &HumanMessageNode{
		NodeInfo: NodeInfo{
			NodeID:          "HumanMessage_" + id.String(),
			NodeName:        "HumanMessage",
			NodeDescription: "Pause the graph until a human message is provided.",
		},
		InterruptMessage: "interrupt due to waiting a human message",
		StateScope:       scopeNodeHumanMessage,
	}
}

func (n *HumanMessageNode) Invoke(ctx context.Context, state State) (State, error) {
	conversation := fruntime.Conversation(state, n.StateScope)
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

func (n *HumanMessageNode) GraphNodeSpec() GraphNodeSpec {
	return GraphNodeSpec{
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
