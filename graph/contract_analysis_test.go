package graph

import (
	"context"
	"strings"
	"testing"
	"weaveflow/builder"
	"weaveflow/builtin"
	"weaveflow/dsl"
	"weaveflow/nodes"
	wfregistry "weaveflow/registry"
	wfstate "weaveflow/state"
)

type staticContractNode struct {
	id   string
	spec dsl.GraphNodeSpec
}

func (n staticContractNode) ID() string          { return n.id }
func (n staticContractNode) Name() string        { return n.id }
func (n staticContractNode) Description() string { return "static contract analysis test node" }
func (n staticContractNode) Execute(ctx context.Context, state wfstate.State) (wfstate.StatePatch, error) {
	_ = ctx
	_ = state
	return wfstate.StatePatch{}, nil
}

func (n staticContractNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return n.spec
}

func registerStaticContractNodeType(registry *wfregistry.Registry, typeName string, contract dsl.StateContract) {
	registry.RegisterNodeType(wfregistry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        typeName,
			Description: "Static contract analysis test node.",
			ConfigSchema: dsl.JSONSchema{
				"type":                 "object",
				"additionalProperties": false,
			},
		},
		ResolveStateContract: func(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
			_ = spec
			return contract.Clone(), nil
		},
		Build: builder.AdaptNodeBuilder(func(ctx *builder.BuildContext, spec dsl.GraphNodeSpec) (nodes.Node, error) {
			_ = ctx
			return staticContractNode{id: spec.ID, spec: spec}, nil
		}),
	})
}

func findDiagnosticByKind(diagnostics []ContractDiagnostic, kind string) *ContractDiagnostic {
	for i := range diagnostics {
		if diagnostics[i].Kind == kind {
			return &diagnostics[i]
		}
	}
	return nil
}

func TestBuildGraphAllowsRequiredReadFromRuntimeInput(t *testing.T) {
	t.Parallel()

	registry := builtin.NewDefaultRegistry()
	registerStaticContractNodeType(registry, "static_reader", dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{Path: "topic", Mode: dsl.StateAccessRead, Required: true},
		},
	})

	graph, err := BuildGraph(registry, dsl.GraphDefinition{
		EntryPoint:  "reader",
		FinishPoint: "reader",
		Nodes: []dsl.GraphNodeSpec{
			{ID: "reader", Type: "static_reader"},
		},
	}, &builder.BuildContext{})
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}
	if diagnostics := graph.ContractDiagnostics(); len(diagnostics) != 0 {
		t.Fatalf("expected no diagnostics, got %#v", diagnostics)
	}
}

func TestBuildGraphRejectsForeignRuntimeRequiredRead(t *testing.T) {
	t.Parallel()

	registry := builtin.NewDefaultRegistry()
	registerStaticContractNodeType(registry, "runtime_reader", dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{Path: "runtime.writer.done", Mode: dsl.StateAccessRead, Required: true},
		},
	})

	_, err := BuildGraph(registry, dsl.GraphDefinition{
		EntryPoint:  "reader",
		FinishPoint: "reader",
		Nodes: []dsl.GraphNodeSpec{
			{ID: "reader", Type: "runtime_reader"},
		},
	}, &builder.BuildContext{})
	if err == nil {
		t.Fatal("expected missing foreign runtime dependency to fail")
	}
	if want := `requires input path "runtime.writer.done"`; err != nil && !strings.Contains(err.Error(), want) {
		t.Fatalf("expected error containing %q, got %v", want, err)
	}
}

func TestBuildGraphAllowsRequiredReadFromUpstreamWriter(t *testing.T) {
	t.Parallel()

	registry := builtin.NewDefaultRegistry()
	registerStaticContractNodeType(registry, "plan_writer", dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{Path: "plan", Mode: dsl.StateAccessWrite},
		},
	})
	registerStaticContractNodeType(registry, "plan_reader", dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{Path: "plan.result", Mode: dsl.StateAccessRead, Required: true},
		},
	})

	graph, err := BuildGraph(registry, dsl.GraphDefinition{
		EntryPoint:  "writer",
		FinishPoint: "reader",
		Nodes: []dsl.GraphNodeSpec{
			{ID: "writer", Type: "plan_writer"},
			{ID: "reader", Type: "plan_reader"},
		},
		Edges: []dsl.GraphEdgeSpec{
			{From: "writer", To: "reader"},
		},
	}, &builder.BuildContext{})
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}
	if diagnostics := graph.ContractDiagnostics(); len(diagnostics) != 0 {
		t.Fatalf("expected no diagnostics, got %#v", diagnostics)
	}
}

func TestBuildGraphRecordsOverlappingWriteDiagnostic(t *testing.T) {
	t.Parallel()

	registry := builtin.NewDefaultRegistry()
	registerStaticContractNodeType(registry, "writer_a", dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{Path: "answer", Mode: dsl.StateAccessWrite},
		},
	})
	registerStaticContractNodeType(registry, "writer_b", dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{Path: "answer.value", Mode: dsl.StateAccessWrite},
		},
	})

	graph, err := BuildGraph(registry, dsl.GraphDefinition{
		EntryPoint:  "left",
		FinishPoint: "right",
		Nodes: []dsl.GraphNodeSpec{
			{ID: "left", Type: "writer_a"},
			{ID: "right", Type: "writer_b"},
		},
		Edges: []dsl.GraphEdgeSpec{
			{From: "left", To: "right"},
		},
	}, &builder.BuildContext{})
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}

	diagnostic := findDiagnosticByKind(graph.ContractDiagnostics(), "overlapping_write")
	if diagnostic == nil {
		t.Fatalf("expected overlapping_write diagnostic, got %#v", graph.ContractDiagnostics())
	}
	if diagnostic.Path != "shared.answer" {
		t.Fatalf("expected shared.answer overlap path, got %#v", diagnostic)
	}
}

func TestBuildGraphRecordsWildcardAndMultipleSourceDiagnostics(t *testing.T) {
	t.Parallel()

	registry := builtin.NewDefaultRegistry()
	registerStaticContractNodeType(registry, "wildcard_writer", dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{Path: "*", Mode: dsl.StateAccessReadWrite},
		},
	})
	registerStaticContractNodeType(registry, "request_writer", dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{Path: "request.input", Mode: dsl.StateAccessWrite},
		},
	})
	registerStaticContractNodeType(registry, "request_reader", dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{Path: "request.input", Mode: dsl.StateAccessRead, Required: true},
		},
	})

	graph, err := BuildGraph(registry, dsl.GraphDefinition{
		EntryPoint:  "wild",
		FinishPoint: "reader",
		Nodes: []dsl.GraphNodeSpec{
			{ID: "wild", Type: "wildcard_writer"},
			{ID: "writer", Type: "request_writer"},
			{ID: "reader", Type: "request_reader"},
		},
		Edges: []dsl.GraphEdgeSpec{
			{From: "wild", To: "writer"},
			{From: "writer", To: "reader"},
		},
	}, &builder.BuildContext{})
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}

	diagnostics := graph.ContractDiagnostics()
	wildcardDiagnostic := findDiagnosticByKind(diagnostics, "wildcard_contract")
	if wildcardDiagnostic == nil {
		t.Fatalf("expected wildcard_contract diagnostic, got %#v", diagnostics)
	}
	multipleSources := findDiagnosticByKind(diagnostics, "multiple_read_sources")
	if multipleSources == nil {
		t.Fatalf("expected multiple_read_sources diagnostic, got %#v", diagnostics)
	}
}

func TestBuildGraphEmitsContractDiagnosticsToBuildContext(t *testing.T) {
	t.Parallel()

	registry := builtin.NewDefaultRegistry()
	registerStaticContractNodeType(registry, "wildcard_reader", dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{Path: "*", Mode: dsl.StateAccessReadWrite},
		},
	})

	var diagnostics []ContractDiagnostic
	graph, err := BuildGraph(registry, dsl.GraphDefinition{
		EntryPoint:  "reader",
		FinishPoint: "reader",
		Nodes: []dsl.GraphNodeSpec{
			{ID: "reader", Type: "wildcard_reader"},
		},
	}, &builder.BuildContext{
		OnContractDiagnostic: func(diagnostic ContractDiagnostic) {
			diagnostics = append(diagnostics, diagnostic)
		},
	})
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}
	if graph == nil {
		t.Fatal("expected graph")
	}
	if len(diagnostics) == 0 {
		t.Fatal("expected build context to receive contract diagnostics")
	}
	if diagnostics[0].Kind != "wildcard_contract" {
		t.Fatalf("expected wildcard_contract diagnostic, got %#v", diagnostics)
	}
}
