package main

import (
	"context"
	"fmt"
	"weaveflow/nodes"
	"weaveflow/runtime"

	"github.com/tmc/langchaingo/llms/openai"
)

func OrchestrationRouterExample() {
	llm, err := openai.New()
	must(err)

	node := nodes.NewOrchestrationRouterNode(llm)
	node.InputPath = "request"
	node.ContextPaths = []string{"intent", "memory_summary"}
	node.AvailableModes = []string{"direct", "planner", "supervisor"}
	node.Instructions = "Choose planner for decomposition-heavy requests, supervisor for multi-agent delegation, and direct for straightforward execution. Use clarification only when required facts are missing."

	state := runtime.State{
		"request": "在 IM 服务中, 消息的路由如何设计",
		"intent": map[string]any{
			"label":      "architecture_design",
			"confidence": 0.94,
			"reasoning":  "The request asks how to design and route orchestration decisions in the framework.",
		},
		"memory_summary": "The framework already has intent_analyzer, planner, human_message, and expression-based routing.",
	}

	fmt.Println("input:")
	fmt.Println(state["request"])

	result, err := node.Invoke(context.Background(), state)
	must(err)

	fmt.Println()
	fmt.Println("orchestration state:")
	fmt.Println(runtime.Orchestration(result).PrettyString())
}
