package main

import (
	"context"
	"fmt"
	"weaveflow/nodes"
	"weaveflow/runtime"

	"weaveflow/llms/openai"
)

func IntentAnalyzerExample() {
	llm, err := openai.New()
	must(err)

	node := nodes.NewIntentAnalyzerNode(llm)
	node.InputPath = "request"
	node.IntentOptions = []string{
		"qa",
		"tool_use",
		"planner",
		"supervisor",
		"clarification",
	}
	node.Instructions = "Prefer the most actionable label. Use clarification when required information is missing."

	state := runtime.State{
		"request": "信令系统是?",
	}

	fmt.Println("input:")
	fmt.Println(state["request"])

	result, err := node.Invoke(context.Background(), state)
	must(err)

	fmt.Println()
	fmt.Println("intent state:")
	fmt.Println(result.Get(runtime.StateKeyIntent).PrettyString())
}
