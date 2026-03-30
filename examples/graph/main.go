package main

import (
	"context"
	"falcon"
	"falcon/tools"
	"fmt"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
)

func main() {
	graph := falcon.NewGraph()

	model, err := openai.New()
	tryPanic(err)

	lootSets := map[string]falcon.Tool{
		"current_time": tools.NewCurrentTime(),
		"calculator":   tools.NewCalculator(),
	}

	llm := falcon.NewLLMNode(model, lootSets)
	llm.StateScope = "agent"

	tryPanic(graph.AddNode(llm))

	toolCall := falcon.NewToolsNode(lootSets)
	toolCall.StateScope = llm.StateScope

	tryPanic(graph.AddNode(toolCall))

	err = graph.AddConditionalEdge(llm.ID(), toolCall.ID(), falcon.LastMessageHasToolCalls(llm.StateScope))
	tryPanic(err)

	err = graph.AddConditionalEdge(llm.ID(), falcon.EndNodeRef, falcon.HasFinalAnswer(llm.StateScope))
	tryPanic(err)

	tryPanic(graph.AddEdge(toolCall.ID(), llm.ID()))

	tryPanic(graph.SetEntryPoint(llm.ID()))

	systemPrompt := "You are a helpful ReAct agent. Use tools when they improve correctness, and return the final answer in plain text."

	messages := make([]llms.MessageContent, 0, 2)
	messages = append(messages, llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt))
	messages = append(messages, llms.TextParts(llms.ChatMessageTypeHuman, "现在上海时间几点？ 顺便计算 (12 + 8) * 3 等于多少。"))
	initialState := falcon.NewBaseState(messages, 10)

	result, err := graph.Run(falcon.WithConsoleEvents(context.Background()), initialState)
	tryPanic(err)

	fmt.Println()
	fmt.Println()
	fmt.Println("====")

	conv := falcon.Conversation(result, llm.StateScope)
	fmt.Println("FinalAnswer:" + conv.FinalAnswer())
}

func tryPanic(error interface{}) {
	if error != nil {
		panic(error)
	}
}
