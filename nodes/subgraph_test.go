package nodes

import (
	"context"
	"testing"
	wfstate "weaveflow/state"
)

func TestSubgraphNodeReturnsStateWhenInvokerMissing(t *testing.T) {
	t.Parallel()

	node := NewSubgraphNode()
	node.GraphRef = "child"

	state := wfstate.State{"topic": "demo"}
	nextState, err := node.Invoke(context.Background(), state)
	if err == nil {
		t.Fatal("expected missing invoker error")
	}
	if nextState["topic"] != "demo" {
		t.Fatalf("expected original state to be returned, got %#v", nextState)
	}
}
