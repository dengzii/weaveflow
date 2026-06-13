package main

import (
	"github.com/dengzii/weaveflow"
	"github.com/dengzii/weaveflow/builtin"
	"github.com/dengzii/weaveflow/memory"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/state"
	"github.com/dengzii/weaveflow/state/accessors"
	"github.com/dengzii/weaveflow/tools"
	"path/filepath"

	"github.com/tmc/langchaingo/llms"
)

const (
	reactAgentStateScope = "agent"
	humanInputFile       = ".local/instance/human_input.txt"
)

func newReActAgentInitialState() *state.State {
	systemPrompt := "你是一个有帮助的 ReAct agent. 当工具提高正确性时使用工具，并以纯文本形式返回最终答案."
	messages := make([]llms.MessageContent, 0, 2)
	messages = append(messages, llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt))
	currentState := state.NewState()
	_ = state.SetPath(currentState, state.Scope(reactAgentStateScope, accessors.KeyConversation, accessors.ConversationFieldMessages).String(), messages)
	_ = state.SetPath(currentState, state.Scope(reactAgentStateScope, accessors.KeyConversation, accessors.ConversationFieldMaxIterations).String(), 10)
	return currentState
}

func newReActAgentTools() map[string]tools.Tool {
	return map[string]tools.Tool{
		"current_time": tools.NewCurrentTime(),
		"calculator":   tools.NewCalculator(),
		//"web_search":   tools.NewWebSearch(),
		"read":      tools.NewRead(),
		"write":     tools.NewWrite(),
		"edit":      tools.NewEdit(),
		"grep":      tools.NewGrep(),
		"web_fetch": tools.NewWebFetch(),
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

	humanInLoop := node.NewHumanMessageNode("")

	tryPanic(graph.AddNode(humanInLoop))

	llm := node.NewLLMNode()

	tryPanic(graph.AddNode(llm))

	toolCall := node.NewToolsNode()

	tryPanic(graph.AddNode(toolCall))

	tryPanic(graph.AddEdge(humanInLoop.ID(), llm.ID()))

	err := graph.AddConditionalEdge(llm.ID(), toolCall.ID(), builtin.LastMessageHasToolCalls())
	tryPanic(err)

	err = graph.AddEdge(toolCall.ID(), llm.ID())
	tryPanic(err)

	err = graph.AddConditionalEdge(llm.ID(), weaveflow.EndNodeRef, builtin.HasFinalAnswer())
	tryPanic(err)

	tryPanic(graph.SetEntryPoint(humanInLoop.ID()))

	return graph
}
