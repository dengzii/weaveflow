package main

import (
	"context"
	"fmt"
	"weaveflow/core"
	"weaveflow/node"
	"weaveflow/state"
	"weaveflow/state/accessors"
	"weaveflow/tools"

	"github.com/tmc/langchaingo/llms"
)

func ToolsExample() {
	toolSet := map[string]tools.Tool{
		"calculator":   tools.NewCalculator(),
		"current_time": tools.NewCurrentTime(),
	}

	svc := &core.Services{Tools: toolSet}
	ctx := core.WithServices(context.Background(), svc)

	toolsNode := node.NewToolsNode()
	toolsNode.Parallel = true

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
	must(state.SetPath(currentState, state.Scope("agent", accessors.KeyConversation, accessors.ConversationFieldMessages).String(), messages))
	must(state.SetPath(currentState, state.Scope("agent", accessors.KeyConversation, accessors.ConversationFieldMaxIterations).String(), 5))
	inputConversation := conversation(currentState, "agent")

	fmt.Println("messages before tool execution:")
	for i, msg := range inputConversation.Messages() {
		fmt.Printf("  [%d] %s: %s\n", i, msg.Role, describeMessage(msg))
	}

	result, err := executeNode(ctx, toolsNode, currentState)
	must(err)

	conv := conversation(result, "agent")
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
