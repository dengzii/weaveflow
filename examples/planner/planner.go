package main

import (
	"context"
	"fmt"
	"weaveflow/nodes"
	"weaveflow/runtime"

	"github.com/tmc/langchaingo/llms/openai"
)

func main() {
	llm, _ := openai.New()
	node := nodes.NewPlannerNode(llm)

	state := runtime.State{}
	plannerState := runtime.EnsurePlanner(state)
	plannerState["objective"] = "什么是 RWKV?"

	fmt.Println(state)
	result, err := node.Invoke(context.Background(), state)
	if err != nil {
		panic(err)
	}

	fmt.Println(result.PrettyString())
}
