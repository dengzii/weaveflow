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
	node.ObjectivePath = "request.goal"
	node.ContextPaths = []string{
		"request.constraints",
		"request.available_nodes",
		"execution.completed_steps",
		"execution.last_failure",
	}
	node.StepKindHints = []string{
		"research",
		"decision",
		"action",
		"validation",
		"human_input",
	}
	node.MaxSteps = 4
	node.Instructions = "Plan only. Do not fabricate execution results. Prefer validation and clarification before irreversible actions."

	request := map[string]any{
		"goal": "Prepare a release plan for shipping a new IM message routing feature.",
		"constraints": []string{
			"Must keep backward compatibility for existing clients.",
			"Need a rollback strategy before production rollout.",
			"Only planner output is needed in this demo. Do not execute the plan.",
		},
		"available_nodes": []string{
			"intent_analyzer",
			"planner",
			"router",
			"validator",
			"human_message",
		},
	}
	execution := map[string]any{
		"completed_steps": []string{
			"Clarified the feature scope with product requirements.",
		},
		"last_failure": "Previous rollout attempt lacked a rollback checklist.",
	}
	state := runtime.State{
		"request":   request,
		"execution": execution,
	}
	state.Ensure(runtime.StateKeyPlanner)["status"] = "draft"

	fmt.Println("input objective:")
	fmt.Println(request["goal"])

	fmt.Println()
	fmt.Println("planner context:")
	printJSON(map[string]any{
		"constraints":     request["constraints"],
		"available_nodes": request["available_nodes"],
		"completed_steps": execution["completed_steps"],
		"last_failure":    execution["last_failure"],
	})

	result, err := node.Invoke(context.Background(), state)
	must(err)

	fmt.Println()
	fmt.Println("planner output:")
	printJSON(result.Get(runtime.StateKeyPlanner))
}
