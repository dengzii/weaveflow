package main

import (
	"context"
	"fmt"
	"weaveflow/core"
	"weaveflow/llms/openai"
	"weaveflow/node"
	"weaveflow/state"
	"weaveflow/state/accessors"
	"weaveflow/tools"

	"github.com/tmc/langchaingo/llms"
)

// ExploreExample runs an ExploreNode standalone: the node reads the latest
// human message from the parent scope, drives its own isolated tool loop
// (read / grep / glob) until the model stops calling tools, then writes
// a structured summary back into the parent scope's final_answer.
func ExploreExample() {
	model, err := openai.New()
	must(err)

	ctx := context.Background()
	ctx = core.WithModel(ctx, model)
	ctx = core.WithTools(ctx, map[string]tools.Tool{
		"read": tools.NewRead(),
		"grep": tools.NewGrep(),
		"glob": tools.NewGlob(),
	})
	coreCtx := core.NewContext(ctx)

	exploreNode := node.NewExploreNode()
	exploreNode.ParentScope = "agent"
	exploreNode.MaxIterations = 8
	exploreNode.ToolResultCap = 4096

	currentState := state.NewState()
	must(state.SetPath(currentState, state.Scope("agent", accessors.KeyConversation, accessors.ConversationFieldMessages).String(), []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "Where is the ExploreNode defined and what tools does it use by default?"),
	}))

	result, err := executeNode(coreCtx, exploreNode, currentState)
	must(err)

	parent := conversation(result, "agent")
	fmt.Println("parent conversation:")
	for i, msg := range parent.Messages() {
		fmt.Printf("  [%d] %s: %s\n", i, msg.Role, describeMessage(msg))
	}

	explore := conversation(result, "explore")
	fmt.Println()
	fmt.Printf("explore iterations used: %d / %d\n", explore.IterationCount(), explore.MaxIterations())
	fmt.Println()
	fmt.Println("final answer (summary written to parent scope):")
	fmt.Println(parent.FinalAnswer())
}
