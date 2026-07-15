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

	langgraph "github.com/smallnest/langgraphgo/graph"
	"github.com/tmc/langchaingo/llms"
)

type ConversationInputNode struct {
	Base
	Role             llms.ChatMessageType
	Content          string
	InterruptMessage string
	InputPath        state.Path
	ConversationPath state.Path
	PendingInputPath state.Path
}

func NewConversationInputNode(options ...NodeOption) *ConversationInputNode {
	target := &ConversationInputNode{
		Base: NewBase(Spec{
			Name:        NodeTypeConversationInput,
			Description: "Append one explicitly bound input value to a conversation.",
		}),
		Role:             llms.ChatMessageTypeHuman,
		InterruptMessage: "interrupt due to waiting for human input",
	}
	applyNodeOptions(&target.Base, options)
	return target
}

func (n *ConversationInputNode) Validate() error {
	if n == nil {
		return fmt.Errorf("conversation input node is nil")
	}
	if err := n.Base.Validate(); err != nil {
		return err
	}
	if n.ConversationPath.Empty() {
		return fmt.Errorf("conversation input node %q requires conversation path", n.ID())
	}
	if strings.TrimSpace(n.Content) == "" && n.InputPath.Empty() && n.PendingInputPath.Empty() {
		return fmt.Errorf("conversation input node %q requires pending input path when content and input are empty", n.ID())
	}
	return nil
}

func (n *ConversationInputNode) GraphNodeSpec() dsl.GraphNodeSpec {
	role := string(n.Role)
	if strings.TrimSpace(role) == "" {
		role = string(llms.ChatMessageTypeHuman)
	}
	return newGraphNodeSpec(n.Base, NodeTypeConversationInput, map[string]any{
		"role": role, "content": n.Content, "interrupt_message": n.InterruptMessage,
	}, map[string]state.Path{
		"input": n.InputPath, "conversation": n.ConversationPath, "pending_input": n.PendingInputPath,
	})
}

func ConversationInputNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type: NodeTypeConversationInput, Title: "Conversation Input",
			Description: "Append a fixed or state-bound value to a conversation before entering an LLM loop.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"role":              dsl.JSONSchema{"type": "string", "enum": []string{"human", "system", "ai", "tool"}, "default": "human"},
					"interrupt_message": dsl.JSONSchema{"type": "string"},
					"content":           dsl.JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
			StatePorts: []dsl.StatePortDefinition{
				primitivePort("input", "Input text appended to the conversation.", "string", dsl.StateAccessRead, false),
				capabilityPort("conversation", "Conversation receiving the message.", conversationcap.CapabilityID, true,
					dsl.RelativeStateFieldRef{Path: conversationcap.FieldMessages, Mode: dsl.StateAccessReadWrite},
					dsl.RelativeStateFieldRef{Path: conversationcap.FieldFinalAnswer, Mode: dsl.StateAccessWrite},
					dsl.RelativeStateFieldRef{Path: conversationcap.FieldIterationCount, Mode: dsl.StateAccessWrite}),
				primitivePort("pending_input", "Optional human input consumed after an interrupt.", "string", dsl.StateAccessReadWrite, false),
			},
		},
		Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (Node, error) {
			spec := resolved.Spec
			conversationPath, err := resolvedPath(resolved, "conversation")
			if err != nil {
				return nil, err
			}
			target := NewConversationInputNode(WithID(spec.ID))
			applyNodeMetadata(&target.Base, spec)
			target.Role = llms.ChatMessageType(strings.TrimSpace(config.String(spec.Config, "role")))
			if target.Role == "" {
				target.Role = llms.ChatMessageTypeHuman
			}
			target.Content = config.String(spec.Config, "content")
			if value := config.String(spec.Config, "interrupt_message"); value != "" {
				target.InterruptMessage = value
			}
			target.InputPath = optionalResolvedPath(resolved, "input")
			target.ConversationPath = conversationPath
			target.PendingInputPath = optionalResolvedPath(resolved, "pending_input")
			if err := target.Validate(); err != nil {
				return nil, err
			}
			return target, nil
		},
	}
}

func (n *ConversationInputNode) Execute(_ core.Context, access *state.Access) error {
	conversation, err := conversationcap.Bind(access, n.ConversationPath)
	if err != nil {
		return err
	}
	if content := strings.TrimSpace(n.Content); content != "" {
		return n.appendInput(conversation, content)
	}
	if !n.InputPath.Empty() {
		input, readErr := state.Get(access, state.NewRef[string](n.InputPath))
		if readErr != nil {
			return readErr
		}
		return n.appendInput(conversation, input)
	}
	if pending, ok, pendingErr := n.consumePendingInput(access); pendingErr != nil {
		return pendingErr
	} else if ok {
		return n.appendInput(conversation, pending)
	}

	messages := conversation.Messages()
	if len(messages) == 0 || messages[len(messages)-1].Role != n.effectiveRole() {
		return &langgraph.NodeInterrupt{Node: n.ID(), Value: n.InterruptMessage}
	}
	return nil
}

func (n *ConversationInputNode) appendInput(conversation *conversationcap.View, content string) error {
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

func (n *ConversationInputNode) effectiveRole() llms.ChatMessageType {
	if n == nil || strings.TrimSpace(string(n.Role)) == "" {
		return llms.ChatMessageTypeHuman
	}
	return n.Role
}

func (n *ConversationInputNode) consumePendingInput(access *state.Access) (string, bool, error) {
	if n.PendingInputPath.Empty() {
		return "", false, nil
	}
	raw, exists := access.ReadAny(n.PendingInputPath)
	if !exists {
		return "", false, nil
	}
	if err := access.Delete(n.PendingInputPath); err != nil {
		return "", false, err
	}
	if raw == nil {
		return "", false, nil
	}
	text, ok := raw.(string)
	if !ok {
		return "", false, fmt.Errorf("state path %q must be string, got %T", n.PendingInputPath.String(), raw)
	}
	text = strings.TrimSpace(text)
	return text, text != "", nil
}

type SetFinalAnswerNode struct {
	Base
	Answer      string
	FromRequest bool
	InputPath   state.Path
	OutputPath  state.Path
}

func NewSetFinalAnswerNode(answer string, options ...NodeOption) *SetFinalAnswerNode {
	target := &SetFinalAnswerNode{Base: NewBase(Spec{Name: "set_final_answer", Description: "Write a final answer."}), Answer: answer}
	applyNodeOptions(&target.Base, options)
	return target
}

func NewRequestToFinalAnswerNode(options ...NodeOption) *SetFinalAnswerNode {
	target := &SetFinalAnswerNode{Base: NewBase(Spec{Name: "request_to_final_answer", Description: "Copy an input value into the final answer."}), FromRequest: true}
	applyNodeOptions(&target.Base, options)
	return target
}

func (n *SetFinalAnswerNode) Validate() error {
	if n == nil {
		return fmt.Errorf("set final answer node is nil")
	}
	if err := n.Base.Validate(); err != nil {
		return err
	}
	if n.OutputPath.Empty() {
		return fmt.Errorf("set final answer node %q requires output path", n.ID())
	}
	if n.FromRequest && n.InputPath.Empty() {
		return fmt.Errorf("set final answer node %q requires input path", n.ID())
	}
	return nil
}

func (n *SetFinalAnswerNode) Execute(_ core.Context, access *state.Access) error {
	answer := n.Answer
	if n.FromRequest {
		value, err := state.Get(access, state.NewRef[string](n.InputPath))
		if err != nil {
			return err
		}
		answer = value
	}
	return state.Replace(access, state.NewRef[string](n.OutputPath), answer)
}

func (n *SetFinalAnswerNode) Contract() state.Contract {
	if n == nil {
		return state.Contract{}
	}
	fields := []state.FieldAccess{{Path: n.OutputPath, Mode: state.AccessWrite, Merge: state.MergeReplace, Type: "string"}}
	if n.FromRequest {
		fields = append(fields, state.FieldAccess{Path: n.InputPath, Mode: state.AccessRead, Required: true, Merge: state.MergeReplace, Type: "string"})
	}
	return state.NewContract(fields...)
}
