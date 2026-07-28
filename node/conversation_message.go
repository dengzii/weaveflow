package node

import (
	"fmt"
	"strings"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"

	"github.com/tmc/langchaingo/llms"
)

type ConversationMessageNode struct {
	Base
	Role             llms.ChatMessageType
	Content          string
	InputPath        state.Path
	ConversationPath state.Path
}

func NewConversationMessageNode(options ...NodeOption) *ConversationMessageNode {
	target := &ConversationMessageNode{
		Base: NewBase(Spec{
			Name:        NodeTypeConversationMessage,
			Description: "Append a fixed or state-bound message to a conversation.",
		}),
		Role: llms.ChatMessageTypeHuman,
	}
	applyNodeOptions(&target.Base, options)
	ApplyDefaultStatePaths(target)
	return target
}

func (n *ConversationMessageNode) Validate() error {
	if n == nil {
		return fmt.Errorf("conversation message node is nil")
	}
	if err := n.Base.Validate(); err != nil {
		return err
	}
	if n.ConversationPath.Empty() {
		return fmt.Errorf("conversation message node %q requires conversation path", n.ID())
	}
	if strings.TrimSpace(n.Content) == "" && n.InputPath.Empty() {
		return fmt.Errorf("conversation message node %q requires content or input path", n.ID())
	}
	return nil
}

func (n *ConversationMessageNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return newGraphNodeSpec(n.Base, NodeTypeConversationMessage, map[string]any{
		"role": string(n.effectiveRole()), "content": n.Content,
	}, map[string]state.Path{
		"input": n.InputPath, "conversation": n.ConversationPath,
	})
}

func ConversationMessageNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type: NodeTypeConversationMessage, Title: "Conversation Message",
			Description: "Append a fixed or state-bound message to an explicitly bound conversation.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"role":    dsl.JSONSchema{"type": "string", "enum": []string{"human", "system", "ai", "tool"}, "default": "human"},
					"content": dsl.JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
			StatePorts: []dsl.StatePortDefinition{
				primitivePort("input", "Text appended to the conversation when content is empty.", "string", dsl.StateAccessRead, false),
				capabilityPort("conversation", "Conversation receiving the message.", conversationcap.CapabilityID, true,
					dsl.RelativeStateFieldRef{Path: conversationcap.FieldMessages, Mode: dsl.StateAccessReadWrite},
					dsl.RelativeStateFieldRef{Path: conversationcap.FieldFinalAnswer, Mode: dsl.StateAccessWrite},
					dsl.RelativeStateFieldRef{Path: conversationcap.FieldIterationCount, Mode: dsl.StateAccessWrite}),
			},
		},
		Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (Node, error) {
			spec := resolved.Spec
			conversationPath, err := resolvedPath(resolved, "conversation")
			if err != nil {
				return nil, err
			}
			target := NewConversationMessageNode(WithID(spec.ID))
			applyNodeMetadata(&target.Base, spec)
			target.Role = llms.ChatMessageType(strings.TrimSpace(config.String(spec.Config, "role")))
			if target.Role == "" {
				target.Role = llms.ChatMessageTypeHuman
			}
			target.Content = config.String(spec.Config, "content")
			target.InputPath = optionalResolvedPath(resolved, "input")
			target.ConversationPath = conversationPath
			if err := target.Validate(); err != nil {
				return nil, err
			}
			return target, nil
		},
	}
}

func (n *ConversationMessageNode) Execute(_ core.Context, access *state.Access) error {
	content := strings.TrimSpace(n.Content)
	if content == "" {
		raw, exists := access.ReadAny(n.InputPath)
		if !exists {
			return fmt.Errorf("state path %q is required", n.InputPath.String())
		}
		input, ok := raw.(string)
		if !ok {
			return fmt.Errorf("state path %q must be string, got %T", n.InputPath.String(), raw)
		}
		content = strings.TrimSpace(input)
		if content == "" {
			return fmt.Errorf("state path %q must contain non-empty text", n.InputPath.String())
		}
	}
	conversation, err := conversationcap.Bind(access, n.ConversationPath)
	if err != nil {
		return err
	}
	role := n.effectiveRole()
	if role == llms.ChatMessageTypeHuman {
		if err := conversation.ResetIteration(); err != nil {
			return err
		}
		if err := conversation.SetFinalAnswer(""); err != nil {
			return err
		}
	}
	return conversation.AppendMessage(llms.TextParts(role, content))
}

func (n *ConversationMessageNode) effectiveRole() llms.ChatMessageType {
	if n == nil || strings.TrimSpace(string(n.Role)) == "" {
		return llms.ChatMessageTypeHuman
	}
	return n.Role
}
