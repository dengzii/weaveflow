package neo

import (
	"path/filepath"

	"weaveflow"
	"weaveflow/memory"
	"weaveflow/nodes"
	fruntime "weaveflow/runtime"
	"weaveflow/tools"

	"github.com/tmc/langchaingo/llms"
)

const stateScope = "agent"

type Config struct {
	StateScope        string
	SystemPrompt      string
	MaxIterations     int
	PlannerMaxSteps   int
	MemoryRecallLimit int
	MemoryRecallTags  []string
}

func DefaultConfig() Config {
	return Config{
		StateScope:        stateScope,
		SystemPrompt:      "你是 Neo，一个通用任务 Agent。你会先规划再执行，使用工具收集信息，最后给出经过验证的答案。",
		MaxIterations:     16,
		PlannerMaxSteps:   6,
		MemoryRecallLimit: 5,
		MemoryRecallTags:  []string{"final_answer", "assistant_output", "user_input"},
	}
}

func NewBuildContext(model llms.Model, baseDir string) *weaveflow.BuildContext {
	repo := memory.NewFileMemoryRepository(filepath.Join(baseDir, "memory"))
	return &weaveflow.BuildContext{
		Model:  model,
		Memory: memory.New(&memory.Options{Repository: repo, Retriever: memory.NewBM25Retriever(repo, nil)}),
		Tools: map[string]tools.Tool{
			"current_time":    tools.NewCurrentTime(),
			"calculator":      tools.NewCalculator(),
			"file_operations": tools.NewFileOperations(),
		},
	}
}

func NewGraph(buildCtx *weaveflow.BuildContext, cfg Config) (*weaveflow.Graph, error) {
	scope := cfg.StateScope
	if scope == "" {
		scope = stateScope
	}

	graph := weaveflow.NewGraph()

	// --- nodes ---

	bootstrap := nodes.NewSessionBootstrapNode()
	bootstrap.StateScope = scope
	bootstrap.MaxIterations = cfg.MaxIterations
	bootstrap.SystemPrompt = cfg.SystemPrompt
	if err := graph.AddNode(bootstrap); err != nil {
		return nil, err
	}

	memRecall := nodes.NewMemoryRecallNode(buildCtx.Memory)
	memRecall.StateScope = scope
	memRecall.Limit = cfg.MemoryRecallLimit
	memRecall.Tags = cfg.MemoryRecallTags
	if err := graph.AddNode(memRecall); err != nil {
		return nil, err
	}

	router := nodes.NewOrchestrationRouterNode(buildCtx.Model)
	router.InputPath = fruntime.StateKeyRequest + ".input"
	router.AvailableModes = []string{"direct", "planner"}
	if err := graph.AddNode(router); err != nil {
		return nil, err
	}

	planner := nodes.NewPlannerNode(buildCtx.Model)
	planner.ContextPaths = []string{fruntime.StateKeyMemory, fruntime.StateKeyExecution}
	planner.MaxSteps = cfg.PlannerMaxSteps
	if err := graph.AddNode(planner); err != nil {
		return nil, err
	}

	stepExec := nodes.NewPlanStepExecutorNode()
	stepExec.StateScope = scope
	if err := graph.AddNode(stepExec); err != nil {
		return nil, err
	}

	ctxAssembler := nodes.NewContextAssemblerNode()
	ctxAssembler.StateScope = scope
	if err := graph.AddNode(ctxAssembler); err != nil {
		return nil, err
	}

	llmNode := nodes.NewLLMNode(buildCtx.Model, buildCtx.Tools)
	llmNode.StateScope = scope
	if err := graph.AddNode(llmNode); err != nil {
		return nil, err
	}

	toolCall := nodes.NewToolCallNode(buildCtx.Tools)
	toolCall.StateScope = scope
	if err := graph.AddNode(toolCall); err != nil {
		return nil, err
	}

	obsRecorder := nodes.NewObservationRecorderNode()
	obsRecorder.StateScope = scope
	if err := graph.AddNode(obsRecorder); err != nil {
		return nil, err
	}

	verifier := nodes.NewVerifierNode(buildCtx.Model)
	verifier.StateScope = scope
	if err := graph.AddNode(verifier); err != nil {
		return nil, err
	}

	finalizer := nodes.NewFinalizerNode(buildCtx.Model)
	finalizer.StateScope = scope
	if err := graph.AddNode(finalizer); err != nil {
		return nil, err
	}

	memWrite := nodes.NewMemoryWriteNode(buildCtx.Memory)
	memWrite.StateScope = scope
	memWrite.MinRequestLength = 8
	memWrite.MinAnswerLength = 12
	memWrite.MinSummaryLength = 20
	if err := graph.AddNode(memWrite); err != nil {
		return nil, err
	}

	// --- edges ---

	// entry: bootstrap → memory_recall → router
	if err := graph.AddEdge(bootstrap.ID(), memRecall.ID()); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(memRecall.ID(), router.ID()); err != nil {
		return nil, err
	}

	// router: mode == "planner" → planner, default → context_assembler (direct mode)
	routeToPlanner, err := weaveflow.ExpressionConditions(weaveflow.ExpressionConditionConfig{
		Expressions: []weaveflow.Expression{{Value1: "orchestration.mode", Op: "equals", Value2: "planner"}},
	})
	if err != nil {
		return nil, err
	}
	if err := graph.AddConditionalEdge(router.ID(), planner.ID(), routeToPlanner); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(router.ID(), ctxAssembler.ID()); err != nil {
		return nil, err
	}

	// planner path: planner → plan_step_executor
	if err := graph.AddEdge(planner.ID(), stepExec.ID()); err != nil {
		return nil, err
	}

	// plan_step_executor conditional routing
	routeFinalize, err := weaveflow.ExpressionConditions(weaveflow.ExpressionConditionConfig{
		Expressions: []weaveflow.Expression{{Value1: "execution.route", Op: "equals", Value2: "finalize"}},
	})
	if err != nil {
		return nil, err
	}
	routeBlocked, err := weaveflow.ExpressionConditions(weaveflow.ExpressionConditionConfig{
		Expressions: []weaveflow.Expression{{Value1: "execution.route", Op: "equals", Value2: "blocked"}},
	})
	if err != nil {
		return nil, err
	}
	if err := graph.AddConditionalEdge(stepExec.ID(), finalizer.ID(), routeFinalize); err != nil {
		return nil, err
	}
	if err := graph.AddConditionalEdge(stepExec.ID(), finalizer.ID(), routeBlocked); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(stepExec.ID(), ctxAssembler.ID()); err != nil {
		return nil, err
	}

	// shared: context_assembler → llm
	if err := graph.AddEdge(ctxAssembler.ID(), llmNode.ID()); err != nil {
		return nil, err
	}

	// llm routing (order matters — conditions evaluated first-to-last):
	// 1. has tool calls → tool_call (both modes)
	if err := graph.AddConditionalEdge(llmNode.ID(), toolCall.ID(), weaveflow.LastMessageHasToolCalls(scope)); err != nil {
		return nil, err
	}
	// 2. direct mode + no tools → finalizer (skip verifier)
	directMode, err := weaveflow.ExpressionConditions(weaveflow.ExpressionConditionConfig{
		Expressions: []weaveflow.Expression{{Value1: "orchestration.mode", Op: "equals", Value2: "direct"}},
	})
	if err != nil {
		return nil, err
	}
	if err := graph.AddConditionalEdge(llmNode.ID(), finalizer.ID(), directMode); err != nil {
		return nil, err
	}
	// 3. default: plan mode + no tools → observation_recorder
	if err := graph.AddEdge(llmNode.ID(), obsRecorder.ID()); err != nil {
		return nil, err
	}

	// tool_call routing:
	// 1. direct mode → context_assembler (tool loop)
	directModeToolLoop, err := weaveflow.ExpressionConditions(weaveflow.ExpressionConditionConfig{
		Expressions: []weaveflow.Expression{{Value1: "orchestration.mode", Op: "equals", Value2: "direct"}},
	})
	if err != nil {
		return nil, err
	}
	if err := graph.AddConditionalEdge(toolCall.ID(), ctxAssembler.ID(), directModeToolLoop); err != nil {
		return nil, err
	}
	// 2. default: plan mode → observation_recorder
	if err := graph.AddEdge(toolCall.ID(), obsRecorder.ID()); err != nil {
		return nil, err
	}

	// plan mode verification loop
	if err := graph.AddEdge(obsRecorder.ID(), verifier.ID()); err != nil {
		return nil, err
	}
	verifyReplan, err := weaveflow.ExpressionConditions(weaveflow.ExpressionConditionConfig{
		Expressions: []weaveflow.Expression{{Value1: "verification.next_action", Op: "equals", Value2: "replan"}},
	})
	if err != nil {
		return nil, err
	}
	verifyFinalize, err := weaveflow.ExpressionConditions(weaveflow.ExpressionConditionConfig{
		Expressions: []weaveflow.Expression{{Value1: "verification.next_action", Op: "equals", Value2: "finalize"}},
	})
	if err != nil {
		return nil, err
	}
	if err := graph.AddConditionalEdge(verifier.ID(), planner.ID(), verifyReplan); err != nil {
		return nil, err
	}
	if err := graph.AddConditionalEdge(verifier.ID(), finalizer.ID(), verifyFinalize); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(verifier.ID(), stepExec.ID()); err != nil {
		return nil, err
	}

	// finalization: finalizer → memory_write → END
	if err := graph.AddEdge(finalizer.ID(), memWrite.ID()); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(memWrite.ID(), weaveflow.EndNodeRef); err != nil {
		return nil, err
	}

	// entry and finish
	if err := graph.SetEntryPoint(bootstrap.ID()); err != nil {
		return nil, err
	}
	if err := graph.SetFinishPoint(memWrite.ID()); err != nil {
		return nil, err
	}

	return graph, nil
}

func NewInitialState(input string) fruntime.State {
	return fruntime.State{
		fruntime.StateKeyRequest: fruntime.State{
			"input": input,
		},
		fruntime.StateKeyPlanner: fruntime.State{
			"objective": input,
		},
	}
}
