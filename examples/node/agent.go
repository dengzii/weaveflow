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
)

// AgentExample runs an AgentNode standalone: one Execute call drives the
// internal ReAct loop (LLM <-> tool) until the model returns a final answer
// or the iteration cap is hit.
func AgentExample() {
	model, err := openai.New()
	must(err)

	svc := &core.Services{
		Model: runtime.WrapLLM(model),
		Tools: map[string]tools.Tool{
			"calculator":   tools.NewCalculator(),
			"current_time": tools.NewCurrentTime(),
		},
	}
	ctx := core.WithServices(context.Background(), svc)

	agent := nodes.NewAgentNode()
	agent.StateScope = "subagent"
	agent.SystemPrompt = "You are a concise assistant. Use tools when they improve accuracy. Return the final answer as plain text."
	agent.ToolIDs = []string{"calculator", "current_time"}
	agent.InputPath = "task"
	agent.OutputPath = "agent_answer"
	agent.MaxIterations = 6

	state := wfstate.State{
		"task": "What is 42 * 58, and what is the current time?",
	}

	result, err := executeNode(ctx, agent, state)
	must(err)

	conv := result.Conversation("subagent")
	fmt.Println("internal conversation:")
	for i, msg := range conv.Messages() {
		fmt.Printf("  [%d] %s: %s\n", i, msg.Role, describeMessage(msg))
	}

	fmt.Println()
	fmt.Printf("iterations used: %d / %d\n", conv.IterationCount(), conv.MaxIterations())
	fmt.Println("final answer:", conv.FinalAnswer())
	if answer, ok := result["agent_answer"].(string); ok {
		fmt.Println("output_path agent_answer:", answer)
	}
}

// AgentAsToolExample shows the agent-as-tool pattern: a sub-agent is wrapped
// via AsTool() and registered into Services.Tools so an outer agent can
// delegate to it like any other tool. Both the coordinator and the specialist
// are AgentNodes — the coordinator's internal loop will actually execute the
// math_agent tool, which in turn runs its own isolated loop on a fresh state.
func AgentAsToolExample() {
	model, err := openai.New()
	must(err)

	// Sub-agent: handles arithmetic questions in isolation.
	subAgent := nodes.NewAgentNode()
	subAgent.StateScope = "math_subagent"
	subAgent.SystemPrompt = "You answer arithmetic questions. Use the calculator tool and return only the numeric result."
	subAgent.ToolIDs = []string{"calculator"}
	subAgent.MaxIterations = 4
	subAgent.ToolName = "math_agent"
	subAgent.ToolDescription = "Delegate an arithmetic question to a specialist sub-agent. Input: {\"task\": \"<question>\"}."

	svc := &core.Services{
		Model: runtime.WrapLLM(model),
		Tools: map[string]tools.Tool{
			"current_time": tools.NewCurrentTime(),
			"math_agent":   subAgent.AsTool(),
		},
	}
	ctx := core.WithServices(context.Background(), svc)

	// Coordinator agent: only has access to current_time + the math_agent
	// tool. When it needs arithmetic, it delegates instead of computing.
	coordinator := nodes.NewAgentNode()
	coordinator.StateScope = "coordinator"
	coordinator.SystemPrompt = "You coordinate by delegating to specialist tools. For arithmetic, call math_agent. Return a plain-text final answer."
	coordinator.ToolIDs = []string{"current_time", "math_agent"}
	coordinator.MaxIterations = 6
	coordinator.InputPath = "task"
	coordinator.OutputPath = "final_answer"

	state := wfstate.State{
		"task": "Please compute 1234 * 5678 and tell me the current time.",
	}

	result, err := executeNode(ctx, coordinator, state)
	must(err)

	conv := result.Conversation("coordinator")
	fmt.Println("\n=> coordinator conversation:")
	for i, msg := range conv.Messages() {
		fmt.Printf("  [%d] %s: %s\n", i, msg.Role, describeMessage(msg))
	}

	fmt.Println()
	fmt.Printf("coordinator iterations used: %d / %d\n", conv.IterationCount(), conv.MaxIterations())
	fmt.Println("coordinator final answer:", conv.FinalAnswer())
	if answer, ok := result["final_answer"].(string); ok {
		fmt.Println("output_path final_answer:", answer)
	}
}
