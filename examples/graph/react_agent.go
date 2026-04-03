package main

import (
	"falcon"
	fruntime "falcon/runtime"

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

func newReActAgentGraph() *falcon.Graph {
	graph := falcon.NewGraph()

	model, err := openai.New()
	tryPanic(err)

	toolSets := map[string]falcon.Tool{
		"current_time": falcon.NewCurrentTime(),
		"calculator":   falcon.NewCalculator(),
	}

	humanInLoop := falcon.NewHumanMessageNode()
	humanInLoop.StateScope = reactAgentStateScope

	tryPanic(graph.AddNode(humanInLoop))

	llm := falcon.NewLLMNode(model, toolSets)
	llm.StateScope = reactAgentStateScope

	tryPanic(graph.AddNode(llm))

	toolCall := falcon.NewToolCallNode(toolSets)
	toolCall.StateScope = llm.StateScope

	tryPanic(graph.AddNode(toolCall))

	tryPanic(graph.AddEdge(humanInLoop.ID(), llm.ID()))

	err = graph.AddConditionalEdge(llm.ID(), toolCall.ID(), falcon.LastMessageHasToolCalls(llm.StateScope))
	tryPanic(err)

	err = graph.AddConditionalEdge(llm.ID(), falcon.EndNodeRef, falcon.HasFinalAnswer(llm.StateScope))
	tryPanic(err)

	tryPanic(graph.AddEdge(toolCall.ID(), llm.ID()))

	tryPanic(graph.SetEntryPoint(humanInLoop.ID()))
	tryPanic(graph.SetFinishPoint(llm.ID()))

	return graph
}
