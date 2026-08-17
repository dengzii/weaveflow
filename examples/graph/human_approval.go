//go:build ignore

package main

import (
	"context"
	"fmt"

	"github.com/dengzii/weaveflow"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	wfgraph "github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

func main() {
	ctx := context.Background()
	componentRegistry := weaveflow.NewDefaultRegistry()
	registerApprovalType(componentRegistry)
	workflow := wfgraph.NewGraph(componentRegistry)

	addApprovalNode(workflow, approvalSpec("prepare", "example_proposal_writer", "proposal", "shared.proposal"), func(_ core.Context, access *state.Access) (core.NodeResult, error) {
		return core.Success(), access.SetAny(state.Shared("proposal"), map[string]any{
			"id":     "change-42",
			"action": "publish release",
		})
	})
	addApprovalNode(workflow, approvalSpec("approval", "example_proposal_reader", "proposal", "shared.proposal"), func(_ core.Context, access *state.Access) (core.NodeResult, error) {
		proposal, _ := access.ReadAny(state.Shared("proposal"))
		return core.NodeResult{
			Command: core.Command{Suspend: &core.SuspendRequest{Value: proposal}},
		}, nil
	})
	addApprovalNode(workflow, approvalSpec("decide", "example_approval_reader", "approved", "shared.approval.approved"), func(_ core.Context, access *state.Access) (core.NodeResult, error) {
		approved, _ := access.ReadAny(state.Shared("approval", "approved"))
		target := core.NodeRef("rejected")
		if approved == true {
			target = "published"
		}
		return core.NodeResult{Command: core.Command{Goto: []core.NodeRef{target}}}, nil
	})
	addApprovalNode(workflow, approvalSpec("published", "example_outcome_writer", "outcome", "shared.outcomes.published"), func(_ core.Context, access *state.Access) (core.NodeResult, error) {
		return core.Success(), access.SetAny(state.Shared("outcomes", "published"), "published")
	})
	addApprovalNode(workflow, approvalSpec("rejected", "example_outcome_writer", "outcome", "shared.outcomes.rejected"), func(_ core.Context, access *state.Access) (core.NodeResult, error) {
		return core.Success(), access.SetAny(state.Shared("outcomes", "rejected"), "rejected")
	})
	addApprovalNode(workflow, dsl.GraphNodeSpec{
		ID: "finish", Type: "example_outcome_finalizer", Name: "finish",
		State: map[string]dsl.StateBinding{
			"published": {Path: "shared.outcomes.published"},
			"rejected":  {Path: "shared.outcomes.rejected"},
			"outcome":   {Path: "shared.outcome"},
		},
	}, func(_ core.Context, access *state.Access) (core.NodeResult, error) {
		outcome, ok := access.ReadAny(state.Shared("outcomes", "published"))
		if !ok {
			outcome, _ = access.ReadAny(state.Shared("outcomes", "rejected"))
		}
		return core.NodeResult{
			Command: core.Command{Return: &core.ReturnCommand{Value: outcome}},
		}, access.SetAny(state.Shared("outcome"), outcome)
	})

	mustApproval(workflow.SetEntryPoint("prepare"))
	mustApproval(workflow.SetFinishPoint("finish"))
	mustApproval(workflow.AddEdge("prepare", "approval"))
	mustApproval(workflow.AddEdge("approval", "decide"))
	mustApproval(workflow.AddEdge("decide", "published"))
	mustApproval(workflow.AddEdge("decide", "rejected"))
	mustApproval(workflow.AddEdge("published", "finish"))
	mustApproval(workflow.AddEdge("rejected", "finish"))

	runner, err := weaveflow.NewInMemoryRunner(workflow)
	mustApproval(err)
	for _, approved := range []bool{true, false} {
		pausedRun, _, startErr := runner.Start(ctx, state.NewState())
		mustApproval(startErr)
		if pausedRun.Status != runtime.RunStatusPaused {
			panic(fmt.Errorf("run status is %q, want %q", pausedRun.Status, runtime.RunStatusPaused))
		}
		fmt.Printf("paused run=%s checkpoint=%s\n", pausedRun.RunID, pausedRun.LastCheckpointID)

		resumeInput := state.FromShared(map[string]any{
			"approval": map[string]any{
				"approved": approved,
				"reviewer": "operator@example.com",
			},
		})
		completedRun, finalState, resumeErr := runner.Resume(ctx, pausedRun.RunID, resumeInput)
		mustApproval(resumeErr)
		outcome, _ := state.ReadPath(finalState, "shared.outcome")
		fmt.Printf("resumed run=%s status=%s approved=%t outcome=%v return=%v\n", completedRun.RunID, completedRun.Status, approved, outcome, completedRun.ReturnValue)
	}
}

func registerApprovalType(componentRegistry *registry.Registry) {
	directBuild := func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (core.Node, error) {
		return nil, fmt.Errorf("example node %q must be constructed directly", resolved.Spec.ID)
	}
	definitions := []registry.NodeTypeDefinition{
		approvalTypeDefinition("example_proposal_writer", "proposal", dsl.JSONSchema{"type": "object"}, dsl.StateAccessWrite, directBuild),
		approvalTypeDefinition("example_proposal_reader", "proposal", dsl.JSONSchema{"type": "object"}, dsl.StateAccessRead, directBuild),
		approvalTypeDefinition("example_approval_reader", "approved", dsl.JSONSchema{"type": "boolean"}, dsl.StateAccessRead, directBuild),
		approvalTypeDefinition("example_outcome_writer", "outcome", dsl.JSONSchema{"type": "string"}, dsl.StateAccessWrite, directBuild),
		{
			NodeTypeSchema: dsl.NodeTypeSchema{
				Type: "example_outcome_finalizer",
				StatePorts: []dsl.StatePortDefinition{
					{Name: "published", Schema: dsl.JSONSchema{"type": "string"}, Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace},
					{Name: "rejected", Schema: dsl.JSONSchema{"type": "string"}, Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace},
					{Name: "outcome", Required: true, Schema: dsl.JSONSchema{"type": "string"}, Mode: dsl.StateAccessWrite, MergeStrategy: dsl.StateMergeReplace},
				},
			},
			Build: directBuild,
		},
	}
	for _, definition := range definitions {
		mustApproval(componentRegistry.RegisterNodeType(definition))
	}
}

func approvalTypeDefinition(nodeType, portName string, schema dsl.JSONSchema, mode dsl.StateAccessMode, build func(*registry.BuildContext, registry.ResolvedNodeSpec) (core.Node, error)) registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type: nodeType,
			StatePorts: []dsl.StatePortDefinition{{
				Name: portName, Required: true, Schema: schema, Mode: mode, MergeStrategy: dsl.StateMergeReplace,
			}},
		},
		Build: build,
	}
}

func approvalSpec(identifier, nodeType, portName, path string) dsl.GraphNodeSpec {
	return dsl.GraphNodeSpec{
		ID: identifier, Type: nodeType, Name: identifier,
		State: map[string]dsl.StateBinding{portName: {Path: path}},
	}
}

func addApprovalNode(workflow interface {
	AddNode(core.Node) error
	SetNodeSpec(dsl.GraphNodeSpec) error
}, spec dsl.GraphNodeSpec, execute node.ExecuteFunc) {
	mustApproval(workflow.AddNode(node.NewFuncNode(node.Spec{ID: spec.ID, Name: spec.Name}, execute)))
	mustApproval(workflow.SetNodeSpec(spec))
}

func mustApproval(err error) {
	if err != nil {
		panic(err)
	}
}
