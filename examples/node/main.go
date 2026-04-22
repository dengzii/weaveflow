package main

import (
	"fmt"
	"time"
)

func main() {
	now := time.Now()
	//SessionBootstrapExample()
	//OrchestrationRouterExample()
	//IntentAnalyzerExample()
	//IteratorExample()
	PlannerExample()
	span := time.Now().Sub(now)
	fmt.Printf("node invoke took %s\n", span)
}
