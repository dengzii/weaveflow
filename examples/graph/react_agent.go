package main

import (
	"github.com/dengzii/weaveflow"
	"github.com/dengzii/weaveflow/builtin"
	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	"github.com/dengzii/weaveflow/core"
	wfgraph "github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/state"
	"github.com/dengzii/weaveflow/tools"

	"github.com/tmc/langchaingo/llms"
)

const (
	humanInputFile = ".local/instance/human_input.txt"
)

var reactAgentConversationPath = state.Scope("agent", "conversation")
var reactAgentPendingInputPath = state.Scope("agent", "pending_input")

func newReActAgentInitialState() *state.State {
	systemPrompt := "你是一个有帮助的 ReAct agent. 当工具提高正确性时使用工具，并以纯文本形式返回最终答案."
	messages := make([]llms.MessageContent, 0, 2)
	messages = append(messages, llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt))
	currentState := state.NewState()
	access := state.NewEditingAccess(currentState)
	conversation, _ := conversationcap.Bind(access, reactAgentConversationPath)
	_ = conversation.SetMessages(messages)
	_ = conversation.SetMaxIterations(10)
	return access.State()
}

func newReActAgentTools() map[string]core.Tool {
	return map[string]core.Tool{
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

func newReActAgentGraph() *wfgraph.Graph {
	graph := weaveflow.NewGraph()

	userInput := node.NewUserInputNode()
	userInput.PendingInputPath = reactAgentPendingInputPath

	tryPanic(graph.AddNode(userInput))

	message := node.NewConversationMessageNode()
	message.InputPath = userInput.ValuePath
	message.ConversationPath = reactAgentConversationPath

	tryPanic(graph.AddNode(message))

	llm := node.NewLLMTurnNode()
	llm.ConversationPath = reactAgentConversationPath

	tryPanic(graph.AddNode(llm))

	toolCall := node.NewToolExecutionNode()
	toolCall.ConversationPath = reactAgentConversationPath

	tryPanic(graph.AddNode(toolCall))

	tryPanic(graph.AddEdge(userInput.ID(), message.ID()))
	tryPanic(graph.AddEdge(message.ID(), llm.ID()))

	err := graph.AddConditionalEdge(llm.ID(), toolCall.ID(), builtin.ConversationHasToolCalls(reactAgentConversationPath))
	tryPanic(err)

	err = graph.AddEdge(toolCall.ID(), llm.ID())
	tryPanic(err)

	err = graph.AddEdge(llm.ID(), weaveflow.EndNodeRef)
	tryPanic(err)

	tryPanic(graph.SetEntryPoint(userInput.ID()))

	return graph
}
