package main

import (
	"context"
	"fmt"
	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/state"
	"github.com/dengzii/weaveflow/tools"

	"github.com/tmc/langchaingo/llms"
)

func ToolExecutionExample() {
	toolSet := map[string]core.Tool{
		"calculator":   tools.NewCalculator(),
		"current_time": tools.NewCurrentTime(),
	}

	ctx := core.NewContext(core.WithTools(context.Background(), toolSet))

	toolExecutionNode := node.NewToolExecutionNode()
	toolExecutionNode.Parallel = true
	conversationPath := state.Scope("agent", "conversation")
	toolExecutionNode.ConversationPath = conversationPath

	currentState := state.NewState()
	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "You are a concise assistant."),
		llms.TextParts(llms.ChatMessageTypeHuman, "What is 42 * 58? And what time is it?"),
		{
			Role: llms.ChatMessageTypeAI,
			Parts: []llms.ContentPart{
				llms.ToolCall{
					ID: "call_001",
					FunctionCall: &llms.FunctionCall{
						Name:      "calculator",
						Arguments: `{"expression":"42 * 58"}`,
					},
				},
				llms.ToolCall{
					ID: "call_002",
					FunctionCall: &llms.FunctionCall{
						Name:      "current_time",
						Arguments: `{}`,
					},
				},
			},
		},
	}
	seedAccess := state.NewEditingAccess(currentState)
	seed, err := conversationcap.Bind(seedAccess, conversationPath)
	must(err)
	must(seed.SetMessages(messages))
	must(seed.SetMaxIterations(5))
	currentState = seedAccess.State()
	inputConversation := conversation(currentState, conversationPath)

	fmt.Println("messages before tool execution:")
	for i, msg := range inputConversation.Messages() {
		fmt.Printf("  [%d] %s: %s\n", i, msg.Role, describeMessage(msg))
	}

	result, err := executeNode(ctx, toolExecutionNode, currentState)
	must(err)

	conv := conversation(result, conversationPath)
	fmt.Println()
	fmt.Println("messages after tool execution:")
	for i, msg := range conv.Messages() {
		fmt.Printf("  [%d] %s: %s\n", i, msg.Role, describeMessage(msg))
	}
}

func describeMessage(msg llms.MessageContent) string {
	for _, part := range msg.Parts {
		switch p := part.(type) {
		case llms.TextContent:
			return p.Text
		case llms.ToolCall:
			if p.FunctionCall != nil {
				return fmt.Sprintf("[tool_call] %s(%s)", p.FunctionCall.Name, p.FunctionCall.Arguments)
			}
		case llms.ToolCallResponse:
			return fmt.Sprintf("[tool_result] %s → %s", p.Name, p.Content)
		}
	}
	return ""
}
