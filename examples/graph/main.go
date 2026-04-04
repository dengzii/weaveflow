package main

import (
	"falcon"
	"falcon/runtime"
	"fmt"
	"time"

	"go.uber.org/zap"
)

func main() {
	logger, _ := zap.NewDevelopment()
	falcon.SetLogger(logger)

	runWithRunner()

	time.Sleep(time.Second)
	fmt.Println("===================")
	resumeFromCheckpoint()
}

func runWithRunner() {
	graph := newReActAgentGraph()
	err := falcon.RunGraphWithRunner(".local/instance", graph, newReActAgentInitialState())
	tryPanic(err)
}

func resumeFromCheckpoint() {
	state := runtime.State{}
	scope := state.EnsureScope(reactAgentStateScope)
	scope[falcon.PendingHumanInputStateKey] = "What is 64+(12*5), and what time is it now?"

	err := falcon.ResumeGraphRunnerFromDirectory(".local/instance", state)
	tryPanic(err)
}

func tryPanic(error interface{}) {
	if error != nil {
		panic(error)
	}
}
