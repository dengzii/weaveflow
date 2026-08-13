package main

import (
	"context"
	"fmt"
	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/state"
	"github.com/dengzii/weaveflow/tools"

	"github.com/dengzii/weaveflow/llms/openai"

	"github.com/dengzii/weaveflow/llms"
)

func LLMTurnExample() {
	model, err := openai.New()
	must(err)

	ctx := context.Background()
	ctx = core.WithModel(ctx, model)
	ctx = core.WithTools(ctx, map[string]core.Tool{
		"calculator":   tools.NewCalculator(),
		"current_time": tools.NewCurrentTime(),
	})
	coreCtx := core.NewContext(ctx)

	llmTurnNode := node.NewLLMTurnNode()
	conversationPath := state.Scope("agent", "conversation")
	llmTurnNode.ConversationPath = conversationPath

	currentState := state.NewState()
	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "You are a concise assistant. Use tools when they improve accuracy."),
		llms.TextParts(llms.ChatMessageTypeHuman, "What is 42 * 58?"),
	}
	seedAccess := state.NewEditingAccess(currentState)
	seed, err := conversationcap.Bind(seedAccess, conversationPath)
	must(err)
	must(seed.SetMessages(messages))
	must(seed.SetMaxIterations(5))
	currentState = seedAccess.State()
	inputConversation := conversation(currentState, conversationPath)

	fmt.Println("input messages:")
	for i, msg := range inputConversation.Messages() {
		fmt.Printf("  [%d] %s: %s\n", i, msg.Role, describeMessage(msg))
	}

	result, err := executeNode(coreCtx, llmTurnNode, currentState)
	must(err)

	conv := conversation(result, conversationPath)
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
