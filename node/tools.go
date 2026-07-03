package node

import (
	"errors"
	"fmt"
	"sync"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
	"github.com/dengzii/weaveflow/state/accessors"

	"github.com/tmc/langchaingo/llms"
)

type ToolsNode struct {
	Base
	ToolIDs  []string
	Parallel bool
}

func NewToolsNode(options ...NodeOption) *ToolsNode {
	node := &ToolsNode{
		Base: NewBase(Spec{
			Name:         NodeTypeTools,
			Description:  "Execute tool calls emitted by the model.",
			Scope:        DefaultScope,
			AccessorUses: []AccessorUse{Use(accessors.ConversationID.Name())},
		}),
		Parallel: true,
	}
	applyNodeOptions(&node.Base, options)
	return node
}

func ToolsNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        NodeTypeTools,
			Title:       "Tools Node",
			Description: "Built-in tool execution nodes.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"tool_ids":    dsl.JSONSchema{"type": "array", "items": dsl.JSONSchema{"type": "string"}},
					"state_scope": dsl.JSONSchema{"type": "string"},
					"parallel":    dsl.JSONSchema{"type": "boolean"},
				},
				"additionalProperties": false,
			},
		},
		ResolveStateContract: func(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
			scope := nodeStateScope(spec.Config)
			return dsl.StateContract{
				Fields: []dsl.StateFieldRef{{Path: scopedConversationPath(scope, accessors.ConversationFieldMessages), Mode: dsl.StateAccessReadWrite, Description: "Conversation messages inspected for tool calls and extended with tool responses."}},
			}, nil
		},
		Build: func(ctx *registry.BuildContext, spec dsl.GraphNodeSpec) (Node, error) {
			_ = ctx
			toolsNode := NewToolsNode(WithScope(nodeStateScope(spec.Config)), WithID(spec.ID))
			applyNodeMetadata(&toolsNode.Base, spec)
			toolsNode.ToolIDs = config.StringSlice(spec.Config, "tool_ids")
			if parallel, ok := config.Bool(spec.Config, "parallel"); ok {
				toolsNode.Parallel = parallel
			}
			return toolsNode, nil
		},
	}
}

func (t *ToolsNode) Execute(ctx core.Context, access *state.Access) error {
	if ctx.Tools() == nil {
		return errors.New("tools node: tools not available")
	}
	conversation, err := state.UseAccessor(access, accessors.ConversationID)
	if err != nil {
		return err
	}

	messages := conversation.Messages()
	if len(messages) == 0 {
		return errors.New("no messages available for tool execution")
	}

	lastMessage := messages[len(messages)-1]
	if lastMessage.Role != llms.ChatMessageTypeAI {
		return errors.New("last message is not an AI message")
	}

	toolCalls := make([]llms.ToolCall, 0, len(lastMessage.Parts))
	for _, part := range lastMessage.Parts {
		toolCall, ok := part.(llms.ToolCall)
		if !ok {
			continue
		}
		toolCalls = append(toolCalls, toolCall)
	}

	toolMessages := make([]llms.MessageContent, len(toolCalls))
	if t.Parallel {
		var wg sync.WaitGroup
		wg.Add(len(toolCalls))
		for index, toolCall := range toolCalls {
			go func(index int, toolCall llms.ToolCall) {
				defer wg.Done()
				toolMessages[index] = executeToolCallMessage(ctx, toolCall)
			}(index, toolCall)
		}
		wg.Wait()
	} else {
		for index, toolCall := range toolCalls {
			toolMessages[index] = executeToolCallMessage(ctx, toolCall)
		}
	}
	return conversation.SetMessages(append(messages, toolMessages...))
}

func executeToolCall(ctx core.Context, toolCall llms.ToolCall) (string, error) {
	if toolCall.FunctionCall == nil {
		return "", errors.New("tool call has no function payload")
	}
	name := toolCall.FunctionCall.Name
	tool, ok := core.FindTool(ctx.Tools(), name)
	if !ok {
		return "", fmt.Errorf("tool %q not found", name)
	}
	if tool.Handler == nil {
		return "", fmt.Errorf("tool handler %q not found", name)
	}
	arguments := toolCall.FunctionCall.Arguments
	callCtx := core.WithToolCallMetadata(ctx, core.ToolCallMetadata{
		ToolCallID: toolCall.ID,
		Name:       name,
		Arguments:  arguments,
	})
	return tool.Handler(callCtx, core.DecodeToolInput(arguments))
}

func executeToolCallMessage(ctx core.Context, toolCall llms.ToolCall) llms.MessageContent {
	name := toolCallName(toolCall)
	result, err := executeToolCall(ctx, toolCall)
	if err != nil {
		result = "tool execution failed: " + err.Error()
	}
	return llms.MessageContent{
		Role: llms.ChatMessageTypeTool,
		Parts: []llms.ContentPart{
			llms.ToolCallResponse{
				ToolCallID: toolCall.ID,
				Name:       name,
				Content:    result,
			},
		},
	}
}

func toolCallName(toolCall llms.ToolCall) string {
	if toolCall.FunctionCall == nil {
		return ""
	}
	return toolCall.FunctionCall.Name
}
