package main

import (
	"context"
	"fmt"
	"weaveflow/nodes"
	"weaveflow/runtime"
	"weaveflow/tools"

	"github.com/tmc/langchaingo/llms"
)

func ToolsExample() {
	toolSet := map[string]tools.Tool{
		"calculator":   tools.NewCalculator(),
		"current_time": tools.NewCurrentTime(),
	}

	svc := &runtime.Services{Tools: toolSet}
	ctx := runtime.WithServices(context.Background(), svc)

	node := nodes.NewToolCallNode()
	node.StateScope = "agent"
	node.Parallel = true

	state := runtime.State{}
	conversation := runtime.Conversation(state, "agent")
	conversation.UpdateMessage([]llms.MessageContent{
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
	})
	conversation.SetMaxIterations(5)

	fmt.Println("messages before tool execution:")
	for i, msg := range conversation.Messages() {
		fmt.Printf("  [%d] %s: %s\n", i, msg.Role, describeMessage(msg))
	}

	result, err := node.Invoke(ctx, state)
	must(err)

	conv := runtime.Conversation(result, "agent")
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
