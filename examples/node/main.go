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
	LLMExample()
	//ToolsExample()
	//HumanMessageExample()
	//ContextReducerExample()
	//SubgraphExample()
	span := time.Now().Sub(now)
	fmt.Printf("node invoke took %s\n", span)
}
