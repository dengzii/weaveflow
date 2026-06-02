package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"weaveflow/internal/utilities"
	"weaveflow/llms/openai"
	"weaveflow/tools"

	"weaveflow"
	"weaveflow/core"
	"weaveflow/internal/neo"
	fruntime "weaveflow/runtime"
	wfstate "weaveflow/state"
)

// Integration example for neo plan mode without booting the HTTP server.
//
// What this exercises:
//   - OrchestrationRouter (locked to "planner" via Config.Mode)
//   - PlannerNode -> PlanStepExecutorNode -> ContextAssembler -> LLM
//     -> ObservationRecorder -> Verifier -> Finalizer loop
//   - planner_progress events emitted along the way

func main() {

	wd, _ := os.Getwd()

	cfg := neo.DefaultConfig()
	cfg.Mode = "planner"
	cfg.MemoryRecallLimit = 0
	cfg.MaxIterations = 20
	cfg.SystemPrompt = "You are an agent named Neo. You are given the following information: " +
		"current workdir: " + wd

	graph, err := neo.NewGraph(cfg)
	must(err)

	model, err := openai.New()
	must(err)

	baseDir := filepath.Join(".local", "neo_plan_example")
	must(os.MkdirAll(baseDir, 0o755))

	//log, err := zap.NewDevelopment()
	//sink := fruntime.NewLoggerEventSink(log)
	sink := utilities.NewPrettyEventLogging(os.Stdout,
		utilities.WithDisabledEventTypes(fruntime.EventCheckpointCreated, fruntime.EventArtifactCreated),
	)

	runner := weaveflow.NewGraphRunner(
		graph,
		fruntime.NewNoopExecutionStore(),
		fruntime.NewNoopCheckpointStore(),
		wfstate.NewJSONStateCodec(wfstate.DefaultStateVersion),
		sink,
	)
	runner.GraphID = "plan-example"

	ctx := core.WithServices(context.Background(), &core.Services{Model: fruntime.WrapLLM(model), Tools: map[string]tools.Tool{
		"read":  tools.NewRead(),
		"write": tools.NewWrite(),
		"edit":  tools.NewEdit(),
		"glob":  tools.NewGlob(),
		"grep":  tools.NewGrep(),
		"bash":  tools.NewBash(),
	}})

	var state wfstate.State

	_, state, err = runner.Start(ctx, neo.NewInitialState("优化项目 readme, 请使用 plan 模式", nil))

	must(err)

	printPlannerState(state)
	printFinalAnswer(state)
}

func printPlannerState(state wfstate.State) {
	planner := state.Get(wfstate.KeyPlanner)
	fmt.Println()
	fmt.Println("=== planner state ===")
	if planner == nil {
		fmt.Println("  (empty)")
		return
	}
	fmt.Printf("  status:          %v\n", planner["status"])
	fmt.Printf("  current_step_id: %v\n", planner["current_step_id"])
	for _, step := range extractSteps(planner["plan"]) {
		fmt.Printf("  step %s [%s] %s\n", step["id"], step["status"], step["title"])
	}
}

func extractSteps(raw any) []map[string]any {
	switch typed := raw.(type) {
	case []map[string]any:
		return typed
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

func printFinalAnswer(state wfstate.State) {
	fmt.Println()
	fmt.Println("=== final answer ===")
	fmt.Println(state.Conversation("agent").FinalAnswer())
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
