package main

import (
	"context"
	"encoding/json"
	"fmt"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/state"
)

func executeNode(ctx context.Context, targetNode node.Node, currentState *state.State) (*state.State, error) {
	result, err := node.Execute(ctx, currentState, targetNode)
	if err != nil {
		return currentState, err
	}
	return result.State, nil
}

func conversation(currentState *state.State, path state.Path) *conversationcap.View {
	conversation, err := conversationcap.Bind(state.NewAccess(currentState), path)
	must(err)
	return conversation
}

func readState(currentState *state.State, path state.Path) (any, bool) {
	return state.NewAccess(currentState).ReadAny(path)
}

func printJSON(value any) {
	data, err := json.MarshalIndent(value, "", "  ")
	must(err)
	fmt.Println(string(data))
}
