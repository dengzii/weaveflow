package main

import (
	"falcon"
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
	err := falcon.ResumeGraphRunnerFromDirectory(".local/instance")
	tryPanic(err)
}

func tryPanic(error interface{}) {
	if error != nil {
		panic(error)
	}
}
