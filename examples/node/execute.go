package main

import (
	"context"
	"encoding/json"
	"fmt"

	"weaveflow/node"
	"weaveflow/state"
	"weaveflow/state/accessors"
)

func executeNode(ctx context.Context, targetNode node.Node, currentState *state.State) (*state.State, error) {
	registry, err := node.NewDefaultRegistry()
	if err != nil {
		return currentState, err
	}
	result, err := node.Execute(ctx, registry, currentState, targetNode)
	if err != nil {
		return currentState, err
	}
	return result.State, nil
}

func conversation(currentState *state.State, scope string) accessors.Conversation {
	registry, err := node.NewDefaultRegistry()
	must(err)
	access := state.NewAccess(registry, currentState).WithScope(scope)
	conversation, err := state.UseAccessor(access, accessors.ConversationID)
	must(err)
	return conversation
}

func readState(currentState *state.State, path state.Path) (any, bool) {
	return state.NewAccess(nil, currentState).ReadAny(path)
}

func printJSON(value any) {
	data, err := json.MarshalIndent(value, "", "  ")
	must(err)
	fmt.Println(string(data))
}
