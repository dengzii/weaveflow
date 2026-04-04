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
	scope[falcon.PendingHumanInputStateKey] = "64+(12*5)答案是什么, 现在是几点?"

	state, err := falcon.ResumeGraphRunnerFromDirectory(".local/instance", state)
	tryPanic(err)

	conv := runtime.Conversation(state, "agent")
	fmt.Println(conv.FinalAnswer())
}

func tryPanic(error interface{}) {
	if error != nil {
		panic(error)
	}
}
