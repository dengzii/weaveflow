package node

import (
	"errors"
	"fmt"
	"sync"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"

	"github.com/dengzii/weaveflow/llms"
)

type ToolExecutionNode struct {
	Base
	ToolIDs          []string
	Parallel         bool
	ConversationPath state.Path
}

func NewToolExecutionNode(options ...NodeOption) *ToolExecutionNode {
	node := &ToolExecutionNode{
		Base: NewBase(Spec{
			Name:        NodeTypeToolExecution,
			Description: "Execute tool calls emitted by the model.",
		}),
		Parallel: true,
	}
	applyNodeOptions(&node.Base, options)
	ApplyDefaultStatePaths(node)
	return node
}

func (t *ToolExecutionNode) Validate() error {
	if t == nil {
		return fmt.Errorf("tool execution node is nil")
	}
	if err := t.Base.Validate(); err != nil {
		return err
	}
	if t.ConversationPath.Empty() {
		return fmt.Errorf("tool execution node %q requires conversation path", t.ID())
	}
	return nil
}

func (t *ToolExecutionNode) GraphNodeSpec() dsl.GraphNodeSpec {
	config := map[string]any{
		"tool_ids": t.ToolIDs,
		"parallel": t.Parallel,
	}
	return newGraphNodeSpec(t.Base, NodeTypeToolExecution, config, map[string]state.Path{"conversation": t.ConversationPath})
}

func ToolExecutionNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        NodeTypeToolExecution,
			Title:       "Tool Execution",
			Description: "Execute tool calls emitted by a model in a bound conversation.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"tool_ids": dsl.JSONSchema{"type": "array", "items": dsl.JSONSchema{"type": "string"}},
					"parallel": dsl.JSONSchema{"type": "boolean"},
				},
				"additionalProperties": false,
			},
		},
		StatePorts: []dsl.StatePortDefinition{
			capabilityPort("conversation", "Conversation messages inspected for calls and extended with tool results.", conversationcap.CapabilityID, true,
				dsl.RelativeStateFieldRef{Path: conversationcap.FieldMessages, Mode: dsl.StateAccessReadWrite}),
		},
		Build: func(ctx *registry.BuildContext, resolved registry.ResolvedNodeSpec) (Node, error) {
			_ = ctx
			spec := resolved.Spec
			conversationPath, err := resolvedPath(resolved, "conversation")
			if err != nil {
				return nil, err
			}
			toolExecutionNode := NewToolExecutionNode(WithID(spec.ID))
			applyNodeMetadata(&toolExecutionNode.Base, spec)
			toolExecutionNode.ToolIDs = config.StringSlice(spec.Config, "tool_ids")
			if parallel, ok := config.Bool(spec.Config, "parallel"); ok {
				toolExecutionNode.Parallel = parallel
			}
			toolExecutionNode.ConversationPath = conversationPath
			return toolExecutionNode, nil
		},
	}
}

func (t *ToolExecutionNode) Execute(ctx core.Context, access *state.Access) (core.NodeResult, error) {
	return core.NodeResult{}, t.execute(ctx, access)
}

func (t *ToolExecutionNode) execute(ctx core.Context, access *state.Access) error {
	if ctx.Tools() == nil {
		return errors.New("tool execution node: tools not available")
	}
	conversation, err := conversationcap.Bind(access, t.ConversationPath)
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
		type toolTask struct {
			index int
			call  llms.ToolCall
		}
		workerCount := len(toolCalls)
		if limit := core.ToolExecutionConcurrencyLimit(ctx); limit > 0 {
			workerCount = min(workerCount, limit)
		}
		tasks := make(chan toolTask)
		var wg sync.WaitGroup
		wg.Add(workerCount)
		for range workerCount {
			go func() {
				defer wg.Done()
				for task := range tasks {
					toolMessages[task.index] = executeToolCallMessage(ctx, task.call)
				}
			}()
		}
		for index, toolCall := range toolCalls {
			tasks <- toolTask{index: index, call: toolCall}
		}
		close(tasks)
		wg.Wait()
	} else {
		for index, toolCall := range toolCalls {
			toolMessages[index] = executeToolCallMessage(ctx, toolCall)
		}
	}
	return conversation.SetMessages(append(messages, toolMessages...))
}

func executeToolCall(ctx core.Context, toolCall llms.ToolCall) (llms.ToolResult, error) {
	if toolCall.FunctionCall == nil {
		return llms.ToolResult{}, errors.New("tool call has no function payload")
	}
	name := toolCall.FunctionCall.Name
	tool, ok := core.FindTool(ctx.Tools(), name)
	if !ok {
		return llms.ToolResult{}, fmt.Errorf("tool %q not found", name)
	}
	if tool.Handler == nil {
		return llms.ToolResult{}, fmt.Errorf("tool handler %q not found", name)
	}
	return core.ExecuteTool(ctx, tool, toolCall)
}

func executeToolCallMessage(ctx core.Context, toolCall llms.ToolCall) llms.MessageContent {
	name := toolCallName(toolCall)
	result, err := executeToolCall(ctx, toolCall)
	if err != nil {
		result.ToolCallID = toolCall.ID
		result.Name = name
		result.IsError = true
		result.ErrorCode = string(core.ClassifyError(err))
		result.ErrorMessage = err.Error()
	}
	return llms.MessageContent{
		Role: llms.ChatMessageTypeTool,
		Parts: []llms.ContentPart{
			result,
		},
	}
}

// ExecuteToolCallMessage executes a tool call and returns its conversation message.
func ExecuteToolCallMessage(ctx core.Context, toolCall llms.ToolCall) llms.MessageContent {
	return executeToolCallMessage(ctx, toolCall)
}

func toolCallName(toolCall llms.ToolCall) string {
	if toolCall.FunctionCall == nil {
		return ""
	}
	return toolCall.FunctionCall.Name
}
