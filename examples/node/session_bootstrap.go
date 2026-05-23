package main

import (
	"context"
	"fmt"
	"weaveflow/nodes"
	wfstate "weaveflow/state"

	"github.com/tmc/langchaingo/llms"
)

func SessionBootstrapExample() {
	node := nodes.NewSessionBootstrapNode()
	node.StateScope = "agent"
	node.Input = "Summarize what the session bootstrap node initializes."
	node.SystemPrompt = "You are a concise engineering agent."
	node.MaxIterations = 5
	node.AgentProfile = map[string]any{
		"name": "demo-agent",
		"role": "runtime example",
	}
	node.RequestMetadata = map[string]any{
		"workspace_id": "local-demo",
		"user_id":      "example-user",
	}
	node.ToolPolicy = map[string]any{
		"mode":          "allowlist",
		"allowed_tools": []any{"calculator", "current_time"},
	}

	state, err := executeNode(context.Background(), node, wfstate.State{})
	must(err)

	conversation := state.Conversation("agent")
	fmt.Printf("max_iterations: %d\n", conversation.MaxIterations())
	for i, message := range conversation.Messages() {
		fmt.Printf("message[%d] %s: %s\n", i, message.Role, nodeMessageText(message))
	}

	fmt.Println()
	fmt.Println("request:")
	printJSON(state.Get(wfstate.StateKeyRequest))

	fmt.Println()
	fmt.Println("agent:")
	printJSON(state.Get(wfstate.StateKeyAgent))

	fmt.Println()
	fmt.Println("tool_policy:")
	printJSON(state.Get(wfstate.StateKeyToolPolicy))
}

func nodeMessageText(message llms.MessageContent) string {
	for _, part := range message.Parts {
		if text, ok := part.(llms.TextContent); ok {
			return text.Text
		}
	}
	return ""
}
