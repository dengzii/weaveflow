package node

import (
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
	"github.com/dengzii/weaveflow/state/accessors"

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

func (n *HumanMessageNode) GraphNodeSpec() dsl.GraphNodeSpec {
	config := map[string]any{
		"state_scope":       n.Scope(),
		"content":           n.Content,
		"interrupt_message": n.InterruptMessage,
	}
	return newGraphNodeSpec(n.Base, NodeTypeHumanMessage, config)
}

func HumanMessageNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        NodeTypeHumanMessage,
			Title:       "Human Message Node",
			Description: "Pause the graph until the latest message in scope is a human message.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"state_scope":       dsl.JSONSchema{"type": "string"},
					"interrupt_message": dsl.JSONSchema{"type": "string"},
					"content":           dsl.JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
		ResolveStateContract: func(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
			scope := nodeStateScope(spec.Config)
			return dsl.StateContract{
				Fields: []dsl.StateFieldRef{
					{Path: scopedConversationPath(scope, accessors.ConversationFieldMessages), Mode: dsl.StateAccessReadWrite, Description: "Conversation messages inspected and updated by the human message node."},
					{Path: scopedStatePath(scope, PendingHumanInputStateKey), Mode: dsl.StateAccessReadWrite, Description: "Pending human input consumed from state before resuming execution."},
				},
			}, nil
		},
		Build: func(ctx *registry.BuildContext, spec dsl.GraphNodeSpec) (Node, error) {
			_ = ctx
			humanNode := NewHumanMessageNode(config.String(spec.Config, "content"), WithScope(nodeStateScope(spec.Config)), WithID(spec.ID))
			applyNodeMetadata(&humanNode.Base, spec)
			if value := config.String(spec.Config, "interrupt_message"); value != "" {
				humanNode.InterruptMessage = value
			}
			return humanNode, nil
		},
	}
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
	if len(messages) == 0 || messages[len(messages)-1].Role != llms.ChatMessageTypeHuman {
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
