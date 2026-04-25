package main

import (
	"context"
	"fmt"
	"weaveflow/nodes"
	"weaveflow/runtime"

	langgraph "github.com/smallnest/langgraphgo/graph"
	"github.com/tmc/langchaingo/llms"
)

func HumanMessageExample() {
	node := nodes.NewHumanMessageNode()
	node.StateScope = "agent"
	node.InterruptMessage = "Waiting for human input..."

	fmt.Println("=== Case 1: interrupt when no human message is pending ===")
	{
		state := runtime.State{}
		conversation := runtime.Conversation(state, "agent")
		conversation.UpdateMessage([]llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, "You are a helpful assistant."),
			llms.TextParts(llms.ChatMessageTypeAI, "I need more information. What is the target environment?"),
		})

		_, err := node.Invoke(context.Background(), state)
		if err != nil {
			var interrupt *langgraph.NodeInterrupt
			if ok := isNodeInterrupt(err, &interrupt); ok {
				fmt.Printf("  interrupted: node=%s value=%s\n", interrupt.Node, interrupt.Value)
			} else {
				must(err)
			}
		}
	}

	fmt.Println()
	fmt.Println("=== Case 2: resume with pending human input ===")
	{
		state := runtime.State{}
		scope := state.EnsureScope("agent")
		scope[nodes.PendingHumanInputStateKey] = "The target environment is Kubernetes on AWS."

		conversation := runtime.Conversation(state, "agent")
		conversation.UpdateMessage([]llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, "You are a helpful assistant."),
			llms.TextParts(llms.ChatMessageTypeAI, "I need more information. What is the target environment?"),
		})

		result, err := node.Invoke(context.Background(), state)
		must(err)

		conv := runtime.Conversation(result, "agent")
		fmt.Println("  messages after resume:")
		for i, msg := range conv.Messages() {
			fmt.Printf("    [%d] %s: %s\n", i, msg.Role, nodeMessageText(msg))
		}
	}
}

func isNodeInterrupt(err error, target **langgraph.NodeInterrupt) bool {
	interrupt, ok := err.(*langgraph.NodeInterrupt)
	if ok {
		*target = interrupt
		return true
	}
	return false
}
