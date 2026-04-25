package main

import (
	"context"
	"fmt"
	"weaveflow/memory"
	"weaveflow/nodes"
	"weaveflow/runtime"

	"github.com/tmc/langchaingo/llms"
)

func ContextAssemblerExample() {
	node := nodes.NewContextAssemblerNode()
	node.StateScope = "agent"
	node.IncludeMemory = true
	node.IncludeOrchestration = true
	node.IncludePlanner = true

	state := runtime.State{
		"memory": map[string]any{
			"recalled": []map[string]any{
				{
					"text": "User prefers concise bullet-point answers.",
					"role": "assistant",
					"type": string(memory.EntryTypeFact),
					"tags": []string{"preference"},
				},
				{
					"text": "Previous session discussed IM message routing.",
					"role": "assistant",
					"type": string(memory.EntryTypeSummary),
					"tags": []string{"final_answer"},
				},
			},
		},
		"orchestration": map[string]any{
			"mode":       "planner",
			"use_memory": true,
		},
		"planner": map[string]any{
			"objective":       "Design a message routing module",
			"status":          "in_progress",
			"current_step_id": "step_1",
			"plan": []any{
				map[string]any{"id": "step_1", "title": "Research existing patterns", "status": "pending", "kind": "research"},
				map[string]any{"id": "step_2", "title": "Draft routing interface", "status": "pending", "kind": "action"},
			},
		},
	}

	conversation := runtime.Conversation(state, "agent")
	conversation.UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "You are an engineering agent."),
		llms.TextParts(llms.ChatMessageTypeHuman, "Design a message routing module for the IM service."),
	})
	conversation.SetMaxIterations(8)

	fmt.Println("messages before assembly:")
	for i, msg := range conversation.Messages() {
		fmt.Printf("  [%d] %s: %s\n", i, msg.Role, nodeMessageText(msg))
	}

	result, err := node.Invoke(context.Background(), state)
	must(err)

	fmt.Println()
	fmt.Println("messages after assembly:")
	assembled := runtime.Conversation(result, "agent")
	for i, msg := range assembled.Messages() {
		fmt.Printf("  [%d] %s: %s\n", i, msg.Role, nodeMessageText(msg))
	}
}
