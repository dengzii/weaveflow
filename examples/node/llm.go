package main

import (
	"context"
	"fmt"
	"weaveflow/core"
	"weaveflow/node"
	"weaveflow/state"
	"weaveflow/state/accessors"
	"weaveflow/tools"

	"weaveflow/llms/openai"

	"github.com/tmc/langchaingo/llms"
)

func LLMExample() {
	model, err := openai.New()
	must(err)

	ctx := context.Background()
	ctx = core.WithModel(ctx, model)
	ctx = core.WithTools(ctx, map[string]tools.Tool{
		"calculator":   tools.NewCalculator(),
		"current_time": tools.NewCurrentTime(),
	})
	coreCtx := core.NewContext(ctx)

	llmNode := node.NewLLMNode()

	currentState := state.NewState()
	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "You are a concise assistant. Use tools when they improve accuracy."),
		llms.TextParts(llms.ChatMessageTypeHuman, "What is 42 * 58?"),
	}
	must(state.SetPath(currentState, state.Scope("agent", accessors.KeyConversation, accessors.ConversationFieldMessages).String(), messages))
	must(state.SetPath(currentState, state.Scope("agent", accessors.KeyConversation, accessors.ConversationFieldMaxIterations).String(), 5))
	inputConversation := conversation(currentState, "agent")

	fmt.Println("input messages:")
	for i, msg := range inputConversation.Messages() {
		fmt.Printf("  [%d] %s: %s\n", i, msg.Role, describeMessage(msg))
	}

	result, err := executeNode(coreCtx, llmNode, currentState)
	must(err)

	conv := conversation(result, "agent")
	fmt.Println()
	fmt.Println("messages after LLM:")
	for i, msg := range conv.Messages() {
		fmt.Printf("  [%d] %s: %s\n", i, msg.Role, describeMessage(msg))
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
