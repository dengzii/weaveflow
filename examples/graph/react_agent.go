package main

import (
	"path/filepath"
	"weaveflow"
	"weaveflow/llms/openai"
	"weaveflow/memory"
	"weaveflow/nodes"
	fruntime "weaveflow/runtime"
	"weaveflow/tools"

	"github.com/tmc/langchaingo/llms"
)

const (
	reactAgentStateScope = "agent"
	humanInputFile       = ".local/instance/human_input.txt"
)

func newReActAgentInitialState() fruntime.State {
	systemPrompt := "你是一个有帮助的 ReAct agent. 当工具提高正确性时使用工具，并以纯文本形式返回最终答案."
	messages := make([]llms.MessageContent, 0, 2)
	messages = append(messages, llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt))
	return fruntime.NewBaseState(messages, 10)
}

func newReActAgentBuildContext() *weaveflow.BuildContext {
	model, err := openai.New()
	tryPanic(err)

	return &weaveflow.BuildContext{
		Model:  fruntime.WrapLLM(model),
		Memory: newReActAgentMemory(),
		Tools:  newReActAgentTools(),
	}
}

func newReActAgentTools() map[string]tools.Tool {
	return map[string]tools.Tool{
		"current_time": tools.NewCurrentTime(),
		"calculator":   tools.NewCalculator(),
		"web_search":   tools.NewWebSearch(),
		"file_read":    tools.NewFileRead(),
		"file_write":   tools.NewFileWrite(),
		"web_fetch":    tools.NewWebFetch(),
	}
}

func newReActAgentMemory() memory.Manager {
	repo := memory.NewFileMemoryRepository(filepath.Join(".local", "instance"))
	return memory.New(&memory.Options{
		Repository: repo,
		Retriever:  memory.NewBM25Retriever(repo, nil),
	})
}

func newReActAgentGraph() *weaveflow.Graph {
	graph := weaveflow.NewGraph()
	buildCtx := newReActAgentBuildContext()

	humanInLoop := nodes.NewHumanMessageNode()
	humanInLoop.StateScope = reactAgentStateScope

	tryPanic(graph.AddNode(humanInLoop))

	llm := nodes.NewLLMNode(buildCtx.Model, buildCtx.Tools)
	llm.StateScope = reactAgentStateScope

	tryPanic(graph.AddNode(llm))

	toolCall := nodes.NewToolCallNode(buildCtx.Tools)
	toolCall.StateScope = llm.StateScope

	tryPanic(graph.AddNode(toolCall))

	tryPanic(graph.AddEdge(humanInLoop.ID(), llm.ID()))

	err := graph.AddConditionalEdge(llm.ID(), toolCall.ID(), weaveflow.LastMessageHasToolCalls(llm.StateScope))
	tryPanic(err)

	err = graph.AddEdge(toolCall.ID(), llm.ID())
	tryPanic(err)

	err = graph.AddConditionalEdge(llm.ID(), weaveflow.EndNodeRef, weaveflow.HasFinalAnswer(llm.StateScope))
	tryPanic(err)

	tryPanic(graph.SetEntryPoint(humanInLoop.ID()))

	return graph
}
