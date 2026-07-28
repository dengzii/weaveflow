package main

import (
	"fmt"
	"time"
)

func main() {
	now := time.Now()
	//SessionBootstrapExample()
	//IntentAnalyzerExample()
	//OrchestrationRouterExample()
	//PlannerExample()
	//IteratorExample()
	//MemoryRecallExample()
	//MemoryWriteExample()
	//ContextAssemblerExample()
	//LLMTurnExample()
	//ToolExecutionExample()
	//ConversationMessageExample()
	//ContextReducerExample()
	//SubgraphExample()
	//AgentExample()
	//AgentAsToolExample()
	ExploreAgentExample()
	span := time.Now().Sub(now)
	fmt.Printf("node invoke took %s\n", span)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
