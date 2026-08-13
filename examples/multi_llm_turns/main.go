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

	"github.com/dengzii/weaveflow/llms"
)

func main() {
	firstModelName := modelName("OPENAI_MODEL_ONE")
	secondModelName := modelName("OPENAI_MODEL_TWO")
	firstModel, err := openai.New(openai.WithModel(firstModelName))
	if err != nil {
		panic(err)
	}
	secondModel, err := openai.New(openai.WithModel(secondModelName))
	if err != nil {
		panic(err)
	}
	input := strings.TrimSpace(strings.Join(os.Args[1:], " "))
	if input == "" {
		input = "Explain explicit state binding in one paragraph."
	}

	workflow, err := graph.NewBuilder(builtin.NewDefaultRegistry()).Build(multiLLMTurnDefinition(), &registry.BuildContext{})
	if err != nil {
		panic(err)
	}
	ctx := core.WithModels(context.Background(), map[string]llms.Model{
		"first":  firstModel,
		"second": secondModel,
	})
	result, err := workflow.Run(ctx, state.FromShared(map[string]any{
		"request": map[string]any{"input": input},
	}))
	if err != nil {
		panic(err)
	}
	answer, _ := state.ReadPath(result, "shared.final.answer")
	fmt.Println(answer)
}

func multiLLMTurnDefinition() dsl.GraphDefinition {
	return dsl.GraphDefinition{
		Version:      dsl.GraphDefinitionVersion,
		Name:         "isolated_multi_llm_turns",
		StateModules: []dsl.StateModuleRef{{Name: builtin.ProtocolsModuleName, Version: builtin.ProtocolsModuleVersion}},
		EntryPoint:   "input_one",
		FinishPoint:  "llm_two",
		Nodes: []dsl.GraphNodeSpec{
			{
				ID: "input_one", Type: node.NodeTypeConversationMessage,
				State: map[string]dsl.StateBinding{
					"input":        {Path: "shared.request.input"},
					"conversation": {Path: "scopes.llm_one.conversation"},
				},
			},
			{
				ID: "llm_one", Type: node.NodeTypeLLMTurn,
				Config: map[string]any{"model_id": "first"},
				State: map[string]dsl.StateBinding{
					"conversation": {Path: "scopes.llm_one.conversation"},
					"output":       {Path: "shared.handoff.llm_one"},
				},
			},
			{
				ID: "input_two", Type: node.NodeTypeConversationMessage,
				Config: map[string]any{"role": "human"},
				State: map[string]dsl.StateBinding{
					"input":        {Path: "shared.handoff.llm_one"},
					"conversation": {Path: "scopes.llm_two.conversation"},
				},
			},
			{
				ID: "llm_two", Type: node.NodeTypeLLMTurn,
				Config: map[string]any{"model_id": "second", "system_prompt": "Review and improve the first model's response."},
				State: map[string]dsl.StateBinding{
					"conversation": {Path: "scopes.llm_two.conversation"},
					"output":       {Path: "shared.final.answer"},
				},
			},
		},
		Edges: []dsl.GraphEdgeSpec{
			{From: "input_one", To: "llm_one"},
			{From: "llm_one", To: "input_two"},
			{From: "input_two", To: "llm_two"},
		},
	}
}

func modelName(key string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("OPENAI_MODEL")); value != "" {
		return value
	}
	panic(key + " or OPENAI_MODEL is required")
}
