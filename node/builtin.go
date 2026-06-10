package node

import (
	"fmt"
	"strings"

	"weaveflow/core"
	"weaveflow/state"
	"weaveflow/state/accessors"

	langgraph "github.com/smallnest/langgraphgo/graph"
	"github.com/tmc/langchaingo/llms"
)

const PendingHumanInputStateKey = "pending_human_input"

type HumanMessageNode struct {
	Base
	Content          string
	InterruptMessage string
}

func NewHumanMessageNode(content string, options ...NodeOption) *HumanMessageNode {
	node := &HumanMessageNode{
		Base: NewBase(Spec{
			Name:         NodeTypeHumanMessage,
			Description:  "Append a human message to the scoped conversation.",
			Scope:        DefaultScope,
			AccessorUses: []AccessorUse{Use(accessors.ConversationID.Name())},
		}),
		Content:          content,
		InterruptMessage: "interrupt due to waiting a human message",
	}
	applyNodeOptions(&node.Base, options)
	return node
}

func (n *HumanMessageNode) Execute(_ core.Context, access *state.Access) error {
	conversation, err := state.UseAccessor(access, accessors.ConversationID)
	if err != nil {
		return err
	}
	if content := strings.TrimSpace(n.Content); content != "" {
		return conversation.AppendMessage(llms.TextParts(llms.ChatMessageTypeHuman, content))
	}
	if pending, ok, err := n.consumePendingInput(access); err != nil {
		return err
	} else if ok {
		return conversation.AppendMessage(llms.TextParts(llms.ChatMessageTypeHuman, pending))
	}

	messages := conversation.Messages()
	if len(messages) == 0 {
		return nil
	}
	lastMessage := messages[len(messages)-1]
	if lastMessage.Role != llms.ChatMessageTypeHuman {
		return &langgraph.NodeInterrupt{Node: n.ID(), Value: n.InterruptMessage}
	}
	return nil
}

func (n *HumanMessageNode) consumePendingInput(access *state.Access) (string, bool, error) {
	path := pendingHumanInputPath(n.Scope())
	raw, exists := access.ReadAny(path)
	if !exists {
		return "", false, nil
	}
	if err := access.Delete(path); err != nil {
		return "", false, err
	}
	if raw == nil {
		return "", false, nil
	}
	text, ok := raw.(string)
	if !ok {
		return "", false, fmt.Errorf("state scope %q field %q must be string, got %T", n.Scope(), PendingHumanInputStateKey, raw)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false, nil
	}
	return text, true, nil
}

func pendingHumanInputPath(scope string) state.Path {
	if strings.TrimSpace(scope) == "" {
		return state.Shared(PendingHumanInputStateKey)
	}
	return state.Scope(scope, PendingHumanInputStateKey)
}

type SetFinalAnswerNode struct {
	Base
	Answer      string
	FromRequest bool
}

func NewSetFinalAnswerNode(answer string, options ...NodeOption) *SetFinalAnswerNode {
	node := &SetFinalAnswerNode{
		Base: NewBase(Spec{
			Name:         "set_final_answer",
			Description:  "Write a final answer.",
			AccessorUses: []AccessorUse{UseRoot(accessors.FinalID.Name())},
		}),
		Answer: answer,
	}
	applyNodeOptions(&node.Base, options)
	return node
}

func NewRequestToFinalAnswerNode(options ...NodeOption) *SetFinalAnswerNode {
	node := &SetFinalAnswerNode{
		Base: NewBase(Spec{
			Name:        "request_to_final_answer",
			Description: "Copy request input into final answer.",
			AccessorUses: []AccessorUse{
				UseRoot(accessors.RequestID.Name()),
				UseRoot(accessors.FinalID.Name()),
			},
		}),
		FromRequest: true,
	}
	applyNodeOptions(&node.Base, options)
	return node
}

func (n *SetFinalAnswerNode) Execute(_ core.Context, access *state.Access) error {
	answer := n.Answer
	if n.FromRequest {
		request, err := state.UseAccessor(access, accessors.RequestID)
		if err != nil {
			return err
		}
		answer = request.Input()
	}
	final, err := state.UseAccessor(access, accessors.FinalID)
	if err != nil {
		return err
	}
	return final.SetAnswer(answer)
}
