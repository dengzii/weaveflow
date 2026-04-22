package main

import (
	"context"
	"fmt"
	"weaveflow"
	fruntime "weaveflow/runtime"

	"github.com/tmc/langchaingo/llms"
)

func SessionBootstrapExample() {
	registry := weaveflow.DefaultRegistry()
	graph, err := registry.BuildGraph(weaveflow.GraphDefinition{
		EntryPoint:  "bootstrap",
		FinishPoint: "bootstrap",
		Nodes: []weaveflow.GraphNodeSpec{
			{
				ID:   "bootstrap",
				Type: "session_bootstrap",
				Config: map[string]any{
					"state_scope":    "agent",
					"input":          "Summarize what the session bootstrap node initializes.",
					"system_prompt":  "You are a concise engineering agent.",
					"max_iterations": 5,
					"agent_profile": map[string]any{
						"name": "demo-agent",
						"role": "runtime example",
					},
					"request_metadata": map[string]any{
						"workspace_id": "local-demo",
						"user_id":      "example-user",
					},
					"tool_policy": map[string]any{
						"mode":          "allowlist",
						"allowed_tools": []any{"calculator", "current_time"},
					},
				},
			},
		},
	}, &weaveflow.BuildContext{})
	must(err)

	state, err := graph.Run(context.Background(), fruntime.State{})
	must(err)

	conversation := fruntime.Conversation(state, "agent")
	fmt.Printf("max_iterations: %d\n", conversation.MaxIterations())
	for i, message := range conversation.Messages() {
		fmt.Printf("message[%d] %s: %s\n", i, message.Role, nodeMessageText(message))
	}

	fmt.Println()
	fmt.Println("request:")
	printJSON(fruntime.Request(state))

	fmt.Println()
	fmt.Println("agent:")
	printJSON(fruntime.Agent(state))

	fmt.Println()
	fmt.Println("tool_policy:")
	printJSON(fruntime.ToolPolicy(state))
}

func nodeMessageText(message llms.MessageContent) string {
	for _, part := range message.Parts {
		if text, ok := part.(llms.TextContent); ok {
			return text.Text
		}
	}
	return ""
}
