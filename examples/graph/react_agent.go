package main

import (
	"falcon"
	"falcon/nodes"
	fruntime "falcon/runtime"
	"falcon/tools"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
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

func newReActAgentBuildContext() *falcon.BuildContext {
	model, err := openai.New()
	tryPanic(err)

	return &falcon.BuildContext{
		Model: model,
		Tools: newReActAgentTools(),
	}
}

func newReActAgentTools() map[string]tools.Tool {
	return map[string]tools.Tool{
		"current_time": tools.NewCurrentTime(),
		"calculator":   tools.NewCalculator(),
	}
}

func newReActAgentGraph() *falcon.Graph {
	graph := falcon.NewGraph()
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

	err := graph.AddConditionalEdge(llm.ID(), toolCall.ID(), falcon.LastMessageHasToolCalls(llm.StateScope))
	tryPanic(err)

	err = graph.AddConditionalEdge(llm.ID(), falcon.EndNodeRef, falcon.HasFinalAnswer(llm.StateScope))
	tryPanic(err)

	tryPanic(graph.AddEdge(toolCall.ID(), llm.ID()))

	tryPanic(graph.SetEntryPoint(humanInLoop.ID()))
	tryPanic(graph.SetFinishPoint(llm.ID()))

	return graph
}
