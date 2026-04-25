package main

import (
	"context"
	"fmt"
	"weaveflow/nodes"
	"weaveflow/runtime"
	"weaveflow/tools"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
)

func LLMExample() {
	model, err := openai.New()
	must(err)

	toolSet := map[string]tools.Tool{
		"calculator":   tools.NewCalculator(),
		"current_time": tools.NewCurrentTime(),
	}

	node := nodes.NewLLMNode(model, toolSet)
	node.StateScope = "agent"

	state := runtime.State{}
	conversation := runtime.Conversation(state, "agent")
	conversation.UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "You are a concise assistant. Use tools when they improve accuracy."),
		llms.TextParts(llms.ChatMessageTypeHuman, "What is 42 * 58?"),
	})
	conversation.SetMaxIterations(5)

	fmt.Println("input messages:")
	for i, msg := range conversation.Messages() {
		fmt.Printf("  [%d] %s: %s\n", i, msg.Role, nodeMessageText(msg))
	}

	result, err := node.Invoke(context.Background(), state)
	must(err)

	conv := runtime.Conversation(result, "agent")
	fmt.Println()
	fmt.Println("messages after LLM:")
	for i, msg := range conv.Messages() {
		fmt.Printf("  [%d] %s: %s\n", i, msg.Role, nodeMessageText(msg))
	}

	if answer := conv.FinalAnswer(); answer != "" {
		fmt.Println()
		fmt.Println("final answer:", answer)
	} else {
		fmt.Println()
		fmt.Println("(no final answer — LLM emitted tool calls)")
		lastMsg := conv.Messages()[len(conv.Messages())-1]
		for _, part := range lastMsg.Parts {
			if tc, ok := part.(llms.ToolCall); ok && tc.FunctionCall != nil {
				fmt.Printf("  tool_call: %s(%s)\n", tc.FunctionCall.Name, tc.FunctionCall.Arguments)
			}
		}
	}
}
