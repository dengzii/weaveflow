package main

import (
	"context"
	"fmt"
	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/llms/openai"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/state"
	"github.com/dengzii/weaveflow/tools"

	"github.com/dengzii/weaveflow/llms"
)

// ExploreAgentExample runs an ExploreAgentNode standalone: the node reads the latest
// human message from the parent scope, drives its own isolated tool loop
// (read / grep / glob) until the model stops calling tools, then writes
// a structured summary back into the parent scope's final_answer.
func ExploreAgentExample() {
	model, err := openai.New()
	must(err)

	ctx := context.Background()
	ctx = core.WithModel(ctx, model)
	ctx = core.WithTools(ctx, map[string]core.Tool{
		"read": tools.NewRead(),
		"grep": tools.NewGrep(),
		"glob": tools.NewGlob(),
	})
	coreCtx := core.NewContext(ctx)

	exploreNode := node.NewExploreAgentNode()
	exploreNode.MaxIterations = 8
	exploreNode.ToolResultCap = 4096
	exploreNode.TaskPath = state.Shared("task")
	exploreNode.ParentConversationPath = state.Scope("agent", "conversation")
	exploreNode.ConversationPath = state.Scope("explore", "conversation")
	exploreNode.ResultPath = state.Shared("explore_result")

	currentState := state.FromShared(map[string]any{"task": "Where is the ExploreAgentNode defined and what tools does it use by default?"})
	seedAccess := state.NewEditingAccess(currentState)
	parentSeed, err := conversationcap.Bind(seedAccess, exploreNode.ParentConversationPath)
	must(err)
	must(parentSeed.SetMessages([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "Where is the ExploreAgentNode defined and what tools does it use by default?"),
	}))
	currentState = seedAccess.State()

	result, err := executeNode(coreCtx, exploreNode, currentState)
	must(err)

	parent := conversation(result, exploreNode.ParentConversationPath)
	fmt.Println("parent conversation:")
	for i, msg := range parent.Messages() {
		fmt.Printf("  [%d] %s: %s\n", i, msg.Role, describeMessage(msg))
	}

	explore := conversation(result, exploreNode.ConversationPath)
	fmt.Println()
	fmt.Printf("explore iterations used: %d / %d\n", explore.IterationCount(), explore.MaxIterations())
	fmt.Println()
	fmt.Println("final answer (summary written to parent scope):")
	fmt.Println(parent.FinalAnswer())
}
