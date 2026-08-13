package node

import (
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/registry"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

type SubgraphNode struct {
	Base
	GraphRef   string
	InputPath  state.Path
	OutputPath state.Path
	RunChild   registry.ChildRunRunner
}

func NewSubgraphNode(options ...NodeOption) *SubgraphNode {
	target := &SubgraphNode{Base: NewBase(Spec{Name: NodeTypeSubgraph, Description: "Invoke another graph with an explicitly bound state snapshot."})}
	applyNodeOptions(&target.Base, options)
	ApplyDefaultStatePaths(target)
	return target
}

func (n *SubgraphNode) Validate() error {
	if n == nil {
		return fmt.Errorf("subgraph node is nil")
	}
	if err := n.Base.Validate(); err != nil {
		return err
	}
	if n.GraphRef == "" {
		return fmt.Errorf("subgraph node %q requires graph_ref", n.ID())
	}
	if n.InputPath.Empty() || n.OutputPath.Empty() {
		return fmt.Errorf("subgraph node %q requires input and output paths", n.ID())
	}
	return nil
}

func (n *SubgraphNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return newGraphNodeSpec(n.Base, NodeTypeSubgraph, map[string]any{"graph_ref": n.GraphRef}, map[string]state.Path{
		"input": n.InputPath, "output": n.OutputPath,
	})
}

func SubgraphNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type: NodeTypeSubgraph, Title: "Subgraph",
			Description: "Invoke another graph using an explicitly bound JSON state snapshot.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object", "properties": dsl.JSONSchema{"graph_ref": dsl.JSONSchema{"type": "string"}},
				"required": []string{"graph_ref"}, "additionalProperties": false,
			},
			StatePorts: []dsl.StatePortDefinition{
				primitivePortWithDefault("input", "State snapshot supplied to the subgraph.", "object", dsl.StateAccessRead, true, "scopes.{node_id}.input"),
				primitivePortWithDefault("output", "State snapshot returned by the subgraph.", "object", dsl.StateAccessWrite, true, "scopes.{node_id}.output"),
			},
		},
		Build: func(ctx *registry.BuildContext, resolved registry.ResolvedNodeSpec) (Node, error) {
			spec := resolved.Spec
			graphRef := config.String(spec.Config, "graph_ref")
			if graphRef == "" {
				return nil, fmt.Errorf("build subgraph node %q: graph_ref is required", spec.ID)
			}
			if ctx == nil || ctx.ChildRunBuilder == nil {
				return nil, fmt.Errorf("build subgraph node %q: child run builder is required", spec.ID)
			}
			runner, err := ctx.ChildRunBuilder(graphRef)
			if err != nil {
				return nil, fmt.Errorf("build subgraph node %q: %w", spec.ID, err)
			}
			inputPath, err := resolvedPath(resolved, "input")
			if err != nil {
				return nil, err
			}
			outputPath, err := resolvedPath(resolved, "output")
			if err != nil {
				return nil, err
			}
			target := NewSubgraphNode(WithID(spec.ID))
			applyNodeMetadata(&target.Base, spec)
			target.GraphRef = graphRef
			target.InputPath = inputPath
			target.OutputPath = outputPath
			target.RunChild = runner
			return target, nil
		},
	}
}

func (n *SubgraphNode) Execute(ctx core.Context, access *state.Access) (core.NodeResult, error) {
	return core.NodeResult{}, n.execute(ctx, access)
}

func (n *SubgraphNode) execute(ctx core.Context, access *state.Access) error {
	if n.RunChild == nil {
		return fmt.Errorf("subgraph node %q has no child runner for graph_ref %q", n.ID(), n.GraphRef)
	}
	raw, ok := access.ReadAny(n.InputPath)
	if !ok {
		return fmt.Errorf("subgraph node %q input path %q is missing", n.ID(), n.InputPath.String())
	}
	input, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("subgraph node %q input path %q must contain an object, got %T", n.ID(), n.InputPath.String(), raw)
	}
	if _, hasShared := input[state.SectionShared]; !hasShared {
		input = map[string]any{state.SectionShared: input}
	}
	subgraphInput := state.FromMap(input)
	metadata, _ := fruntime.RunnerMetadataFromContext(ctx)
	request := fruntime.ChildRunRequest{
		ParentRunID: metadata.RunID, ParentStepID: metadata.StepID, ParentTaskID: metadata.TaskID,
		GraphRef: n.GraphRef, Namespace: strings.Trim(metadata.Namespace+"/"+n.ID()+":"+metadata.TaskID, "/"),
	}
	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventSubgraphStarted, request)
	childResult, err := n.RunChild(ctx, request, subgraphInput)
	if err != nil {
		_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventSubgraphFailed, map[string]any{"graph_ref": n.GraphRef, "error": err.Error(), "parent_run_id": request.ParentRunID})
		return err
	}
	if childResult.Interrupted {
		return &core.NodeInterrupt{NodeID: n.ID(), Value: fruntime.ChildRunInterrupt{
			RunID: childResult.Run.RunID, CheckpointID: childResult.Run.LastCheckpointID,
			Namespace: childResult.Run.Namespace, Status: string(childResult.Run.Status),
		}}
	}
	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventSubgraphFinished, map[string]any{"graph_ref": n.GraphRef, "child_run_id": childResult.Run.RunID, "namespace": childResult.Run.Namespace})
	if childResult.State == nil {
		return fmt.Errorf("subgraph node %q child run %q returned nil state", n.ID(), childResult.Run.RunID)
	}
	return access.SetAny(n.OutputPath, childResult.State.Export())
}

func (n *SubgraphNode) Contract() state.Contract {
	if n == nil {
		return state.Contract{}
	}
	return state.NewContract(
		state.FieldAccess{Path: n.InputPath, Mode: state.AccessRead, Required: true, Merge: state.MergeReplace, Type: "object"},
		state.FieldAccess{Path: n.OutputPath, Mode: state.AccessWrite, Merge: state.MergeReplace, Type: "object"},
	)
}
