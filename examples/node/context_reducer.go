package main

import (
	"context"
	"fmt"
	"weaveflow/nodes"
	"weaveflow/runtime"

	"weaveflow/llms/openai"

	"github.com/tmc/langchaingo/llms"
)

func ContextReducerExample() {
	model, err := openai.New()
	must(err)

	svc := &runtime.Services{Model: model}
	ctx := runtime.WithServices(context.Background(), svc)

	node := nodes.NewContextReducerNode()
	node.StateScope = "agent"
	node.MaxMessages = 6
	node.PreserveSystem = true
	node.PreserveRecent = 2

	state := runtime.State{}
	conversation := state.Conversation("agent")
	conversation.UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "You are a concise engineering agent."),
		llms.TextParts(llms.ChatMessageTypeHuman, "How does the session bootstrap node work?"),
		llms.TextParts(llms.ChatMessageTypeAI, "It initializes the request, agent profile, tool policy, and scoped conversation."),
		llms.TextParts(llms.ChatMessageTypeHuman, "What about the intent analyzer?"),
		llms.TextParts(llms.ChatMessageTypeAI, "It classifies user intent into labels like qa, tool_use, planner, supervisor, or clarification with confidence scores."),
		llms.TextParts(llms.ChatMessageTypeHuman, "How does the planner decompose objectives?"),
		llms.TextParts(llms.ChatMessageTypeAI, "It gathers context from state paths and asks the LLM to produce steps with dependencies, kinds, and acceptance criteria."),
		llms.TextParts(llms.ChatMessageTypeHuman, "Can I use the iterator node with the planner output?"),
	})

	fmt.Printf("messages before reduction: %d\n", len(conversation.Messages()))
	for i, msg := range conversation.Messages() {
		fmt.Printf("  [%d] %s: %s\n", i, msg.Role, nodeMessageText(msg))
	}

	result, err := node.Invoke(ctx, state)
	must(err)

	conv := result.Conversation("agent")
	fmt.Println()
	fmt.Printf("messages after reduction: %d\n", len(conv.Messages()))
	for i, msg := range conv.Messages() {
		fmt.Printf("  [%d] %s: %s\n", i, msg.Role, nodeMessageText(msg))
	}
}
