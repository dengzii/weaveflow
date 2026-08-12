package main

import (
	"context"
	"fmt"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/llms/openai"
	agentnode "github.com/dengzii/weaveflow/node/agents/agent"
	"github.com/dengzii/weaveflow/state"
	"github.com/dengzii/weaveflow/tools"
)

// AgentExample runs an agent.Node standalone: one Execute call drives the
// internal ReAct loop (LLM <-> tool) until the model returns a final answer
// or the iteration cap is hit.
func AgentExample() {
	model, err := openai.New()
	must(err)

	ctx := context.Background()
	ctx = core.WithModel(ctx, model)
	ctx = core.WithTools(ctx, map[string]core.Tool{
		"calculator":   tools.NewCalculator(),
		"current_time": tools.NewCurrentTime(),
	})
	coreCtx := core.NewContext(ctx)

	target := agentnode.NewNode()
	target.SystemPrompt = "You are a concise assistant. Use tools when they improve accuracy. Return the final answer as plain text."
	target.ToolIDs = []string{"calculator", "current_time"}
	target.TaskPath = state.Shared("task")
	target.ConversationPath = state.Scope("subagent", "conversation")
	target.ResultPath = state.Shared("agent_answer")
	target.MaxIterations = 6

	currentState := state.FromShared(map[string]any{"task": "What is 42 * 58, and what is the current time?"})

	result, err := executeNode(coreCtx, target, currentState)
	must(err)

	conv := conversation(result, target.ConversationPath)
	fmt.Println("internal conversation:")
	for i, msg := range conv.Messages() {
		fmt.Printf("  [%d] %s: %s\n", i, msg.Role, describeMessage(msg))
	}

	fmt.Println()
	fmt.Printf("iterations used: %d / %d\n", conv.IterationCount(), conv.MaxIterations())
	fmt.Println("final answer:", conv.FinalAnswer())
	if answer, ok := readState(result, state.Shared("agent_answer")); ok {
		fmt.Println("agent result:", answer)
	}
}

// AgentAsToolExample shows the agent-as-tool pattern: a specialist agent is
// created with agent.NewTool and registered in the runtime tool set so an outer agent can
// delegate to it like any other tool. The coordinator is an agent.Node, while
// math_agent owns only agent.Config and runs with isolated state.
func AgentAsToolExample() {
	model, err := openai.New()
	must(err)

	mathAgent, err := agentnode.NewTool(agentnode.ToolConfig{
		Name:        "math_agent",
		Description: "Delegate an arithmetic question to a specialist sub-agent.",
		Agent: agentnode.Config{
			SystemPrompt:  "You answer arithmetic questions. Use the calculator tool and return only the numeric result.",
			ToolIDs:       []string{"calculator"},
			MaxIterations: 4,
		},
	})
	must(err)

	ctx := context.Background()
	ctx = core.WithModel(ctx, model)
	ctx = core.WithTools(ctx, map[string]core.Tool{
		"current_time": tools.NewCurrentTime(),
		"math_agent":   mathAgent,
		"calculator":   tools.NewCalculator(),
	})
	coreCtx := core.NewContext(ctx)

	// Coordinator agent: only has access to current_time + the math_agent
	// tool. When it needs arithmetic, it delegates instead of computing.
	coordinator := agentnode.NewNode()
	coordinator.SystemPrompt = "You coordinate by delegating to specialist tools. For arithmetic, call math_agent. Return a plain-text final answer."
	coordinator.ToolIDs = []string{"current_time", "math_agent"}
	coordinator.MaxIterations = 6
	coordinator.TaskPath = state.Shared("task")
	coordinator.ConversationPath = state.Scope("coordinator", "conversation")
	coordinator.ResultPath = state.Shared("final_answer")

	currentState := state.FromShared(map[string]any{"task": "Please compute 1234 * 5678 and tell me the current time."})

	result, err := executeNode(coreCtx, coordinator, currentState)
	must(err)

	conv := conversation(result, coordinator.ConversationPath)
	fmt.Println("\n=> coordinator conversation:")
	for i, msg := range conv.Messages() {
		fmt.Printf("  [%d] %s: %s\n", i, msg.Role, describeMessage(msg))
	}

	fmt.Println()
	fmt.Printf("coordinator iterations used: %d / %d\n", conv.IterationCount(), conv.MaxIterations())
	fmt.Println("coordinator final answer:", conv.FinalAnswer())
	if answer, ok := readState(result, state.Shared("final_answer")); ok {
		fmt.Println("final result:", answer)
	}
}
