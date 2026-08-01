package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/dengzii/weaveflow/builtin"
	supervisorcap "github.com/dengzii/weaveflow/capability/supervisor"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	wfgraph "github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/llms/openai"
	supervisornode "github.com/dengzii/weaveflow/node/supervisor"
	"github.com/dengzii/weaveflow/state"
	"github.com/dengzii/weaveflow/tools"
)

const defaultObjective = "Prepare a short decision note comparing saving $250 per month for 3 years with saving $400 per month for 2 years. Calculate total contributions and explain the main non-numeric tradeoffs."

//go:embed graph.json
var supervisorGraphJSON []byte

func main() {
	objective := strings.TrimSpace(strings.Join(os.Args[1:], " "))
	if objective == "" {
		objective = defaultObjective
	}

	model, err := openai.New()
	must(err)
	graph, err := newSupervisorGraph()
	must(err)

	ctx := core.WithModel(context.Background(), model)
	ctx = core.WithTools(ctx, map[string]core.Tool{
		"calculator":   tools.NewCalculator(),
		"current_time": tools.NewCurrentTime(),
		"web_fetch":    tools.NewWebFetch(),
	})
	initial := state.FromShared(map[string]any{
		"request": map[string]any{"input": objective},
	})
	finalState, err := graph.Run(ctx, initial)
	must(err)

	fmt.Println("objective:", objective)
	if raw, ok := state.ReadPath(finalState, state.Shared("supervisor", supervisornode.SupervisorFieldHistory).String()); ok {
		fmt.Println("delegations:")
		for _, turn := range decodeSupervisorHistory(raw) {
			fmt.Printf("  %d. %s\n     task: %s\n     result: %s\n", turn.Turn, turn.WorkerID, turn.Task, turn.Result)
		}
	}
	answer, _ := state.ReadPath(finalState, state.Shared("final", "answer").String())
	fmt.Println("\nfinal answer:")
	fmt.Println(answer)
}

func newSupervisorGraph() (*wfgraph.Graph, error) {
	definition, err := dsl.DeserializeGraphDefinition(supervisorGraphJSON)
	if err != nil {
		return nil, fmt.Errorf("decode embedded supervisor graph: %w", err)
	}
	return wfgraph.NewBuilder(builtin.NewDefaultRegistry()).Build(definition, nil)
}

func decodeSupervisorHistory(raw any) []supervisorcap.Turn {
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var history []supervisorcap.Turn
	if err := json.Unmarshal(data, &history); err != nil {
		return nil
	}
	return history
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
