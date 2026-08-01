package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/dengzii/weaveflow/builtin"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/llms/openai"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"

	"github.com/tmc/langchaingo/llms"
)

func main() {
	model, err := openai.New()
	if err != nil {
		panic(err)
	}
	task := strings.TrimSpace(strings.Join(os.Args[1:], " "))
	if task == "" {
		task = "Research why explicit state bindings improve multi-agent isolation, then write a concise answer."
	}

	workflow, err := graph.NewBuilder(builtin.NewDefaultRegistry()).Build(twoAgentDefinition(), &registry.BuildContext{})
	if err != nil {
		panic(err)
	}
	ctx := core.WithModels(context.Background(), map[string]llms.Model{
		"research": model,
		"writer":   model,
	})
	result, err := workflow.Run(ctx, state.FromShared(map[string]any{
		"request": map[string]any{"input": task},
	}))
	if err != nil {
		panic(err)
	}
	answer, _ := state.ReadPath(result, "shared.final.answer")
	fmt.Println(answer)
}

func twoAgentDefinition() dsl.GraphDefinition {
	return dsl.GraphDefinition{
		Version:      dsl.GraphDefinitionVersion,
		Name:         "two_agent_handoff",
		StateModules: protocolModules(),
		EntryPoint:   "researcher",
		FinishPoint:  "writer",
		Nodes: []dsl.GraphNodeSpec{
			{
				ID: "researcher", Type: node.NodeTypeAgent,
				Config: map[string]any{
					"model_id":      "research",
					"system_prompt": "Research the task and return factual notes for another agent.",
				},
				State: map[string]dsl.StateBinding{
					"task":         {Path: "shared.request.input"},
					"conversation": {Path: "scopes.researcher.conversation"},
					"result":       {Path: "shared.handoff.research"},
				},
			},
			{
				ID: "writer", Type: node.NodeTypeAgent,
				Config: map[string]any{
					"model_id":      "writer",
					"system_prompt": "Turn the research notes into a concise final response.",
				},
				State: map[string]dsl.StateBinding{
					"task":         {Path: "shared.handoff.research"},
					"conversation": {Path: "scopes.writer.conversation"},
					"result":       {Path: "shared.final.answer"},
				},
			},
		},
		Edges: []dsl.GraphEdgeSpec{{From: "researcher", To: "writer"}},
	}
}

func protocolModules() []dsl.StateModuleRef {
	return []dsl.StateModuleRef{{Name: builtin.ProtocolsModuleName, Version: builtin.ProtocolsModuleVersion}}
}
