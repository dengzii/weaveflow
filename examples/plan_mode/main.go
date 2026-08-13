package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/dengzii/weaveflow"
	"github.com/dengzii/weaveflow/builtin"
	"github.com/dengzii/weaveflow/core"
	wfgraph "github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/llms/openai"
	"github.com/dengzii/weaveflow/node"
	plannode "github.com/dengzii/weaveflow/node/plan"
	"github.com/dengzii/weaveflow/state"
	"github.com/dengzii/weaveflow/tools"

	"github.com/dengzii/weaveflow/llms"
)

const defaultObjective = "Calculate 125 * 48 and briefly explain how the result was verified."

var (
	planObjectivePath    = state.Shared("request", "input")
	planStatePath        = state.Shared("plan")
	planExecutionPath    = state.Shared("execution")
	planConversationPath = state.Scope("plan_worker", "conversation")
	planResultPath       = state.Shared("final", "answer")
)

func main() {
	model, err := openai.New()
	if err != nil {
		panic(err)
	}
	objective := strings.TrimSpace(strings.Join(os.Args[1:], " "))
	if objective == "" {
		objective = defaultObjective
	}
	finalState, err := runPlan(context.Background(), model, planTools(), objective)
	if err != nil {
		panic(err)
	}
	printPlanResult(finalState)
}

func newPlanGraph() (*wfgraph.Graph, error) {
	graph := weaveflow.NewGraph()

	generator := plannode.NewGeneratorNode(node.WithID("generate_plan"))
	generator.ToolIDs = []string{"calculator", "current_time"}
	generator.MaxSteps = 5
	generator.MaxReplans = 1
	generator.ObjectivePath = planObjectivePath
	generator.PlanPath = planStatePath
	generator.ExecutionPath = planExecutionPath

	step := plannode.NewStepNode(node.WithID("prepare_step"))
	step.MaxIterations = 4
	step.PlanPath, step.ExecutionPath, step.ConversationPath = planStatePath, planExecutionPath, planConversationPath

	execute := node.NewLLMTurnNode(node.WithID("execute_step"))
	execute.ToolIDs = []string{"calculator", "current_time"}
	execute.ConversationPath = planConversationPath

	executeTools := node.NewToolExecutionNode(node.WithID("execute_tools"))
	executeTools.ToolIDs = []string{"calculator", "current_time"}
	executeTools.Parallel = true
	executeTools.ConversationPath = planConversationPath

	review := plannode.NewReviewNode(node.WithID("review_step"))
	review.PlanPath, review.ExecutionPath, review.ConversationPath = planStatePath, planExecutionPath, planConversationPath
	synthesis := plannode.NewSynthesisNode(node.WithID("synthesize_plan"))
	synthesis.PlanPath, synthesis.ResultPath = planStatePath, planResultPath

	for _, target := range []node.Node{generator, step, execute, executeTools, review, synthesis} {
		if err := graph.AddNode(target); err != nil {
			return nil, err
		}
	}
	if err := graph.SetEntryPoint(generator.ID()); err != nil {
		return nil, err
	}
	if err := graph.SetFinishPoint(synthesis.ID()); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(generator.ID(), step.ID()); err != nil {
		return nil, err
	}
	if err := graph.AddConditionalEdge(step.ID(), execute.ID(), plannode.StatusEquals(planStatePath, plannode.PlanStatusExecuting)); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(step.ID(), synthesis.ID()); err != nil {
		return nil, err
	}
	if err := graph.AddConditionalEdge(execute.ID(), executeTools.ID(), builtin.ConversationHasToolCalls(planConversationPath)); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(execute.ID(), review.ID()); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(executeTools.ID(), execute.ID()); err != nil {
		return nil, err
	}
	if err := graph.AddConditionalEdge(review.ID(), generator.ID(), plannode.StatusEquals(planStatePath, plannode.PlanStatusReplan)); err != nil {
		return nil, err
	}
	if err := graph.AddConditionalEdge(review.ID(), step.ID(), plannode.StatusEquals(planStatePath, plannode.PlanStatusExecuting)); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(review.ID(), synthesis.ID()); err != nil {
		return nil, err
	}
	return graph, nil
}

func runPlan(ctx context.Context, model llms.Model, availableTools map[string]core.Tool, objective string) (*state.State, error) {
	if model == nil {
		return nil, errors.New("plan mode: model is required")
	}
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return nil, errors.New("plan mode: objective is required")
	}
	graph, err := newPlanGraph()
	if err != nil {
		return nil, err
	}
	ctx = core.WithModel(ctx, model)
	ctx = core.WithTools(ctx, availableTools)
	initial := state.FromShared(map[string]any{
		"request": map[string]any{
			"input": objective,
		},
	})
	return graph.Run(ctx, initial)
}

func planTools() map[string]core.Tool {
	return map[string]core.Tool{
		"calculator":   tools.NewCalculator(),
		"current_time": tools.NewCurrentTime(),
	}
}

func printPlanResult(finalState *state.State) {
	answer, _ := state.ReadPath(finalState, planResultPath.String())
	fmt.Printf("Final answer:\n%s\n", answer)
	plan, _ := state.ReadPath(finalState, planStatePath.String())
	encoded, err := json.MarshalIndent(plan, "", "  ")
	if err == nil {
		fmt.Printf("\nPlan state:\n%s\n", encoded)
	}
}
