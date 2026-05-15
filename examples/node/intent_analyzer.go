package main

import (
	"context"
	"fmt"
	"weaveflow/nodes"
	"weaveflow/runtime"
	wfstate "weaveflow/state"

	"weaveflow/llms/openai"
)

func IntentAnalyzerExample() {
	llm, err := openai.New()
	must(err)

	svc := &runtime.Services{Model: llm}
	ctx := runtime.WithServices(context.Background(), svc)

	node := nodes.NewIntentAnalyzerNode()
	node.InputPath = "request"
	node.IntentOptions = []string{
		"qa",
		"tool_use",
		"planner",
		"supervisor",
		"clarification",
	}
	node.Instructions = "Prefer the most actionable label. Use clarification when required information is missing."

	state := wfstate.State{
		"request": "信令系统是?",
	}

	fmt.Println("input:")
	fmt.Println(state["request"])

	result, err := executeNode(ctx, node, state)
	must(err)

	fmt.Println()
	fmt.Println("intent state:")
	fmt.Println(result.Get(wfstate.StateKeyIntent).PrettyString())
}
