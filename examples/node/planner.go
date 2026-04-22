package main

import (
	"context"
	"fmt"
	"weaveflow/nodes"
	"weaveflow/runtime"

	"github.com/tmc/langchaingo/llms/openai"
)

func PlannerExample() {
	llm, err := openai.New()
	must(err)

	node := nodes.NewPlannerNode(llm)
	node.MaxSteps = 3

	state := runtime.State{}
	plannerState := runtime.EnsurePlanner(state)
	plannerState["objective"] = "什么是快乐星球"

	fmt.Println("input planner state:")
	printJSON(plannerState)

	result, err := node.Invoke(context.Background(), state)
	must(err)

	fmt.Println()
	fmt.Println("planner state:")
	printJSON(runtime.Planner(result))
}
