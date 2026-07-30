package node

import (
	"fmt"
	"strings"

	chatcap "github.com/dengzii/weaveflow/capability/chat"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

type ChatReplyNode struct {
	Base
	Kind      chatcap.ReplyKind
	Content   string
	InputPath state.Path
}

func NewChatReplyNode(options ...NodeOption) *ChatReplyNode {
	target := &ChatReplyNode{
		Base: NewBase(Spec{
			Name:        NodeTypeChatReply,
			Description: "Send an update or standalone message through the active chat trigger.",
		}),
		Kind: chatcap.ReplyMessage,
	}
	applyNodeOptions(&target.Base, options)
	ApplyDefaultStatePaths(target)
	return target
}

func (n *ChatReplyNode) Validate() error {
	if n == nil {
		return fmt.Errorf("chat reply node is nil")
	}
	if err := n.Base.Validate(); err != nil {
		return err
	}
	if n.Kind != chatcap.ReplyUpdate && n.Kind != chatcap.ReplyMessage {
		return fmt.Errorf("chat reply node %q has invalid kind %q", n.ID(), n.Kind)
	}
	if strings.TrimSpace(n.Content) == "" && n.InputPath.Empty() {
		return fmt.Errorf("chat reply node %q requires content or input path", n.ID())
	}
	return nil
}

func (n *ChatReplyNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return newGraphNodeSpec(n.Base, NodeTypeChatReply, map[string]any{
		"kind": string(n.Kind), "content": n.Content,
	}, map[string]state.Path{"input": n.InputPath})
}

func ChatReplyNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type: NodeTypeChatReply, Title: "Chat Reply",
			Description: "Send a streaming update or a separate message through a chat trigger.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"kind":    dsl.JSONSchema{"type": "string", "enum": []string{string(chatcap.ReplyUpdate), string(chatcap.ReplyMessage)}, "default": string(chatcap.ReplyMessage)},
					"content": dsl.JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
			StatePorts: []dsl.StatePortDefinition{
				primitivePortWithDefault("input", "Text sent when content is empty.", "string", dsl.StateAccessRead, false, "shared.final.answer"),
			},
		},
		Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (Node, error) {
			spec := resolved.Spec
			target := NewChatReplyNode(WithID(spec.ID))
			applyNodeMetadata(&target.Base, spec)
			target.Kind = chatcap.ReplyKind(strings.TrimSpace(config.String(spec.Config, "kind")))
			if target.Kind == "" {
				target.Kind = chatcap.ReplyMessage
			}
			target.Content = config.String(spec.Config, "content")
			target.InputPath = optionalResolvedPath(resolved, "input")
			if err := target.Validate(); err != nil {
				return nil, err
			}
			return target, nil
		},
	}
}

func (n *ChatReplyNode) Execute(ctx core.Context, access *state.Access) error {
	content := strings.TrimSpace(n.Content)
	if content == "" {
		raw, exists := access.ReadAny(n.InputPath)
		if !exists {
			return fmt.Errorf("state path %q is required", n.InputPath.String())
		}
		value, ok := raw.(string)
		if !ok {
			return fmt.Errorf("state path %q must be string, got %T", n.InputPath.String(), raw)
		}
		content = strings.TrimSpace(value)
		if content == "" {
			return fmt.Errorf("state path %q must contain non-empty text", n.InputPath.String())
		}
	}
	return chatcap.EmitReply(ctx, chatcap.Reply{Kind: n.Kind, Content: content, NodeID: n.ID()})
}
