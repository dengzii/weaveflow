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
	"github.com/dengzii/weaveflow/tools"
)

func main() {
	model, err := openai.New()
	if err != nil {
		panic(err)
	}
	input := strings.TrimSpace(strings.Join(os.Args[1:], " "))
	if input == "" {
		input = "Use the calculator to evaluate 125 * 48, then explain the result."
	}

	workflow, err := graph.NewBuilder(builtin.NewDefaultRegistry()).Build(sharedToolLoopDefinition(), &registry.BuildContext{})
	if err != nil {
		panic(err)
	}
	ctx := core.WithModel(context.Background(), model)
	ctx = core.WithTools(ctx, map[string]core.Tool{"calculator": tools.NewCalculator()})
	result, err := workflow.Run(ctx, state.FromShared(map[string]any{
		"request": map[string]any{"input": input},
	}))
	if err != nil {
		panic(err)
	}
	answer, _ := state.ReadPath(result, "shared.final.answer")
	fmt.Println(answer)
}

func sharedToolLoopDefinition() dsl.GraphDefinition {
	const conversationPath = "shared.tool_loop"
	return dsl.GraphDefinition{
		Version:      dsl.GraphDefinitionVersion,
		Name:         "shared_llm_turn_tool_execution_condition_loop",
		StateModules: []dsl.StateModuleRef{{Name: builtin.ProtocolsModuleName, Version: builtin.ProtocolsModuleVersion}},
		EntryPoint:   "input",
		Nodes: []dsl.GraphNodeSpec{
			{
				ID: "input", Type: node.NodeTypeConversationMessage,
				State: map[string]dsl.StateBinding{
					"input":        {Path: "shared.request.input"},
					"conversation": {Path: conversationPath},
				},
			},
			{
				ID: "llm", Type: node.NodeTypeLLMTurn,
				Config: map[string]any{"tool_ids": []string{"calculator"}},
				State: map[string]dsl.StateBinding{
					"conversation": {Path: conversationPath},
					"output":       {Path: "shared.final.answer"},
				},
			},
			{
				ID: "tools", Type: node.NodeTypeToolExecution,
				Config: map[string]any{"tool_ids": []string{"calculator"}, "parallel": false},
				State:  map[string]dsl.StateBinding{"conversation": {Path: conversationPath}},
			},
		},
		Edges: []dsl.GraphEdgeSpec{
			{From: "input", To: "llm"},
			{
				From: "llm", To: "tools",
				Condition: &dsl.GraphConditionSpec{
					Type:  builtin.ConditionTypeConversationHasToolCalls,
					State: map[string]dsl.StateBinding{"conversation": {Path: conversationPath}},
				},
			},
			{From: "llm", To: dsl.EndNodeRef},
			{From: "tools", To: "llm"},
		},
	}
}
