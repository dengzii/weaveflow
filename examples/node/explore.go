package main

import (
	"context"
	"fmt"
	"weaveflow/core"
	"weaveflow/llms/openai"
	"weaveflow/nodes"
	"weaveflow/runtime"
	wfstate "weaveflow/state"
	"weaveflow/tools"

	"github.com/tmc/langchaingo/llms"
)

// ExploreExample runs an ExploreNode standalone: the node reads the latest
// human message from the parent scope, drives its own isolated tool loop
// (file_read / grep / glob) until the model stops calling tools, then writes
// a structured summary back into the parent scope's final_answer.
func ExploreExample() {
	model, err := openai.New()
	must(err)

	svc := &core.Services{
		Model: runtime.WrapLLM(model),
		Tools: map[string]tools.Tool{
			"file_read": tools.NewFileRead(),
			"grep":      tools.NewGrep(),
			"glob":      tools.NewGlob(),
		},
	}
	ctx := core.WithServices(context.Background(), svc)

	node := nodes.NewExploreNode()
	node.ParentScope = "agent"
	node.MaxIterations = 8
	node.ToolResultCap = 4096

	state := wfstate.State{}
	state.Conversation("agent").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "Where is the ExploreNode defined and what tools does it use by default?"),
	})

	result, err := executeNode(ctx, node, state)
	must(err)

	parent := result.Conversation("agent")
	fmt.Println("parent conversation:")
	for i, msg := range parent.Messages() {
		fmt.Printf("  [%d] %s: %s\n", i, msg.Role, describeMessage(msg))
	}

	explore := result.Conversation("explore")
	fmt.Println()
	fmt.Printf("explore iterations used: %d / %d\n", explore.IterationCount(), explore.MaxIterations())
	fmt.Println()
	fmt.Println("final answer (summary written to parent scope):")
	fmt.Println(parent.FinalAnswer())
}
