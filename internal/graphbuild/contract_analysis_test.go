package graphbuild

import (
	"testing"

	"github.com/dengzii/weaveflow/state"
)

func TestAnalyzeInitialStateRequirementsSeparatesInitialAndUpstreamPaths(t *testing.T) {
	inputPath := state.Shared("request", "input")
	planPath := state.Shared("plan")

	result := AnalyzeInitialStateRequirements(ContractAnalysisGraph{
		EntryPoint:        "entry",
		EndNode:           "__end__",
		InitialStatePaths: []string{state.Shared("request").String()},
		Edges: map[string][]string{
			"entry":  {"writer"},
			"writer": {"reader"},
		},
		NodeContracts: map[string]state.Contract{
			"entry": state.NewContract(state.FieldAccess{
				Path:        inputPath,
				Mode:        state.AccessRead,
				Required:    true,
				Type:        "string",
				Description: "User request input.",
			}),
			"writer": state.NewContract(state.FieldAccess{
				Path: planPath,
				Mode: state.AccessWrite,
			}),
			"reader": state.NewContract(state.FieldAccess{
				Path:        planPath,
				Mode:        state.AccessRead,
				Required:    true,
				Description: "Generated plan.",
			}),
		},
	})

	if len(result.Required) != 1 {
		t.Fatalf("required = %#v, want one item", result.Required)
	}
	if result.Required[0].Path != inputPath.String() {
		t.Fatalf("required path = %q, want %q", result.Required[0].Path, inputPath.String())
	}
	if len(result.Required[0].Nodes) != 1 || result.Required[0].Nodes[0] != "entry" {
		t.Fatalf("required nodes = %#v, want [entry]", result.Required[0].Nodes)
	}
	if result.Required[0].Type != "string" {
		t.Fatalf("required type = %q, want string", result.Required[0].Type)
	}

	if len(result.ProvidedByUpstream) != 1 {
		t.Fatalf("provided_by_upstream = %#v, want one item", result.ProvidedByUpstream)
	}
	if result.ProvidedByUpstream[0].Path != planPath.String() {
		t.Fatalf("provided path = %q, want %q", result.ProvidedByUpstream[0].Path, planPath.String())
	}
	if len(result.ProvidedByUpstream[0].Nodes) != 1 || result.ProvidedByUpstream[0].Nodes[0] != "reader" {
		t.Fatalf("provided nodes = %#v, want [reader]", result.ProvidedByUpstream[0].Nodes)
	}
	if len(result.ProvidedByUpstream[0].Sources) != 1 || result.ProvidedByUpstream[0].Sources[0] != "writer" {
		t.Fatalf("provided sources = %#v, want [writer]", result.ProvidedByUpstream[0].Sources)
	}
	if len(result.Unresolved) != 0 {
		t.Fatalf("unresolved = %#v, want empty", result.Unresolved)
	}
}

func TestAnalyzeInitialStateRequirementsReportsUnresolvedRuntimePath(t *testing.T) {
	runtimePath := state.Runtime("other", "value")

	result := AnalyzeInitialStateRequirements(ContractAnalysisGraph{
		EntryPoint: "entry",
		EndNode:    "__end__",
		NodeContracts: map[string]state.Contract{
			"entry": state.NewContract(state.FieldAccess{
				Path:     runtimePath,
				Mode:     state.AccessRead,
				Required: true,
			}),
		},
	})

	if len(result.Required) != 0 {
		t.Fatalf("required = %#v, want empty", result.Required)
	}
	if len(result.ProvidedByUpstream) != 0 {
		t.Fatalf("provided_by_upstream = %#v, want empty", result.ProvidedByUpstream)
	}
	if len(result.Unresolved) != 1 || result.Unresolved[0].Path != runtimePath.String() {
		t.Fatalf("unresolved = %#v, want %q", result.Unresolved, runtimePath.String())
	}
}
