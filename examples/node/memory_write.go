package main

import (
	"context"
	"fmt"
	"weaveflow/memory"
	"weaveflow/nodes"
	"weaveflow/runtime"

	"github.com/tmc/langchaingo/llms"
)

func MemoryWriteExample() {
	mgr := memory.New(nil)

	svc := &runtime.Services{Memory: mgr}
	ctx := runtime.WithServices(context.Background(), svc)

	node := nodes.NewMemoryWriteNode()
	node.IncludeRequest = true
	node.IncludeFinalAnswer = true
	node.IncludeSummary = true
	node.Deduplicate = true
	node.MinRequestLength = 5
	node.MinAnswerLength = 10

	state := runtime.State{
		"request": map[string]any{
			"input": "Explain how the planner node decomposes objectives into steps.",
		},
		"planner": map[string]any{
			"objective":       "Explain planner decomposition",
			"status":          "completed",
			"current_step_id": "step_3",
			"summary":         "Decomposed the objective into three research-decision-action steps and validated the plan.",
		},
	}

	conversation := state.Conversation("")
	conversation.UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "You are a concise engineering agent."),
		llms.TextParts(llms.ChatMessageTypeHuman, "Explain how the planner node decomposes objectives into steps."),
		llms.TextParts(llms.ChatMessageTypeAI, "The planner node takes an objective, gathers context from configured state paths, and asks the LLM to produce a list of steps with dependencies, kinds, and acceptance criteria."),
	})
	conversation.SetFinalAnswer("The planner node takes an objective, gathers context from configured state paths, and asks the LLM to produce a list of steps with dependencies, kinds, and acceptance criteria.")

	fmt.Println("input request:")
	fmt.Println(state["request"])

	result, err := node.Invoke(ctx, state)
	must(err)

	fmt.Println()
	fmt.Println("memory write state:")
	printJSON(result.Get(runtime.StateKeyMemory))

	entries, err := mgr.Load(nil)
	must(err)
	fmt.Println()
	fmt.Printf("persisted entries: %d\n", len(entries))
	for i, entry := range entries {
		fmt.Printf("  [%d] role=%s type=%s tags=%v text=%s\n", i, entry.Role, entry.Type, entry.Tags, truncate(entry.Text, 80))
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
