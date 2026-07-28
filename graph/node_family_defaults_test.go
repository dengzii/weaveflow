package graph

import (
	"testing"

	"github.com/dengzii/weaveflow/core"
	plannode "github.com/dengzii/weaveflow/node/plan"
	supervisornode "github.com/dengzii/weaveflow/node/supervisor"
	"github.com/dengzii/weaveflow/state"
)

func TestAddNodeAppliesSubpackageDefaultsAfterAutomaticID(t *testing.T) {
	t.Parallel()
	target := NewGraph()
	firstPlanStep := plannode.NewPlanStepNode()
	secondPlanStep := plannode.NewPlanStepNode()
	worker := supervisornode.NewSupervisorWorkerNode()

	for _, node := range []core.Node{firstPlanStep, secondPlanStep, worker} {
		if err := target.AddNode(node); err != nil {
			t.Fatalf("add node: %v", err)
		}
	}

	if firstPlanStep.ID() == secondPlanStep.ID() {
		t.Fatalf("plan step ids are not unique: %q", firstPlanStep.ID())
	}
	if got, want := firstPlanStep.ConversationPath.String(), state.Scope(firstPlanStep.ID(), "conversation").String(); got != want {
		t.Fatalf("first plan step conversation path = %q, want %q", got, want)
	}
	if got, want := secondPlanStep.ConversationPath.String(), state.Scope(secondPlanStep.ID(), "conversation").String(); got != want {
		t.Fatalf("second plan step conversation path = %q, want %q", got, want)
	}
	if got, want := worker.ConversationPath.String(), state.Scope(worker.ID(), "conversation").String(); got != want {
		t.Fatalf("supervisor worker conversation path = %q, want %q", got, want)
	}
}
