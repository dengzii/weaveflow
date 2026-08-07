package graphbuild

import (
	"testing"

	"github.com/dengzii/weaveflow/core"
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

func TestAnalyzeInitialStateRequirementsClassifiesConcreteEntryProvider(t *testing.T) {
	t.Parallel()
	inputPath := state.Shared("request", "input")
	result := AnalyzeInitialStateRequirements(ContractAnalysisGraph{
		EntryPoint: "entry",
		EndNode:    "__end__",
		EntryProvider: &core.EntryStateProvider{
			ID: "trigger:hook",
			Contract: state.NewContract(state.FieldAccess{
				Path: inputPath,
				Mode: state.AccessWrite,
			}),
		},
		NodeContracts: map[string]state.Contract{
			"entry": state.NewContract(state.FieldAccess{
				Path:     inputPath,
				Mode:     state.AccessRead,
				Required: true,
				Type:     "string",
			}),
		},
	})

	if len(result.Required) != 0 || len(result.Unresolved) != 0 {
		t.Fatalf("requirements = %#v, want entry-provided path", result)
	}
	if len(result.ProvidedByEntry) != 1 {
		t.Fatalf("provided_by_entry = %#v, want one item", result.ProvidedByEntry)
	}
	provided := result.ProvidedByEntry[0]
	if provided.Path != inputPath.String() || len(provided.Sources) != 1 || provided.Sources[0] != "trigger:hook" {
		t.Fatalf("provided_by_entry = %#v", provided)
	}
}

func TestAnalyzeInitialStateRequirementsDistinguishesInputNodeFromRunInput(t *testing.T) {
	t.Parallel()
	inputPath := state.Shared("request", "input")
	analysis := ContractAnalysisGraph{
		EntryPoint:        "input",
		EndNode:           "__end__",
		InitialStatePaths: []string{inputPath.String()},
		Edges: map[string][]string{
			"input": {"agent"},
			"agent": {"__end__"},
		},
		NodeContracts: map[string]state.Contract{
			"input": state.NewContract(state.FieldAccess{
				Path: inputPath,
				Mode: state.AccessReadWrite,
			}),
			"agent": state.NewContract(state.FieldAccess{
				Path:     inputPath,
				Mode:     state.AccessRead,
				Required: true,
				Type:     "string",
			}),
		},
	}

	requirements := AnalyzeInitialStateRequirements(analysis)
	if len(requirements.Required) != 0 {
		t.Fatalf("required = %#v, want empty", requirements.Required)
	}
	if len(requirements.ProvidedByUpstream) != 1 {
		t.Fatalf("provided_by_upstream = %#v, want one item", requirements.ProvidedByUpstream)
	}
	provided := requirements.ProvidedByUpstream[0]
	if provided.Path != inputPath.String() || len(provided.Sources) != 1 || provided.Sources[0] != "input" {
		t.Fatalf("provided_by_upstream = %#v, want source node input", provided)
	}

	diagnostics := AnalyzeContractDiagnostics(analysis)
	if len(diagnostics) != 1 || diagnostics[0].Kind != "multiple_read_sources" {
		t.Fatalf("diagnostics = %#v, want one multiple_read_sources warning", diagnostics)
	}
	if got := diagnostics[0].Sources; len(got) != 2 || got[0] != "run_input" || got[1] != "input" {
		t.Fatalf("diagnostic sources = %#v, want [run_input input]", got)
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

func TestAnalyzeContractDiagnosticsReportsMissingNodeAndConditionProducers(t *testing.T) {
	t.Parallel()
	missingNodePath := state.Shared("missing", "node")
	missingConditionPath := state.Shared("missing", "condition")
	result := AnalyzeContractDiagnostics(ContractAnalysisGraph{
		EntryPoint: "entry",
		EndNode:    "__end__",
		Edges:      map[string][]string{"entry": {"reader"}, "reader": {"__end__"}},
		NodeContracts: map[string]state.Contract{
			"entry": {},
			"reader": state.NewContract(state.FieldAccess{
				Path: missingNodePath, Mode: state.AccessRead, Required: true, Merge: state.MergeReplace,
			}),
		},
		ConditionContracts: map[string]state.Contract{
			"entry": state.NewContract(state.FieldAccess{
				Path: missingConditionPath, Mode: state.AccessRead, Required: true, Merge: state.MergeReplace,
			}),
		},
	})

	assertDiagnostic(t, result, "missing_required_read", "reader", missingNodePath.String())
	assertDiagnostic(t, result, "missing_condition_read", "entry", missingConditionPath.String())
	if err := ContractDiagnosticsError(result); err != nil {
		t.Fatalf("missing producer warnings failed contract validation: %v", err)
	}
}

func TestAnalyzeContractDiagnosticsTracksParallelWavesBeyondInitialFanOut(t *testing.T) {
	t.Parallel()
	path := state.Shared("answer")
	result := AnalyzeContractDiagnostics(ContractAnalysisGraph{
		EntryPoint: "router",
		EndNode:    "__end__",
		Edges: map[string][]string{
			"router":       {"left", "right"},
			"left":         {"left_writer"},
			"right":        {"right_writer"},
			"left_writer":  {"__end__"},
			"right_writer": {"__end__"},
		},
		NodeContracts: map[string]state.Contract{
			"router": {}, "left": {}, "right": {},
			"left_writer":  state.NewContract(state.FieldAccess{Path: path, Mode: state.AccessWrite, Merge: state.MergeReplace}),
			"right_writer": state.NewContract(state.FieldAccess{Path: path, Mode: state.AccessWrite, Merge: state.MergeReplace}),
		},
	})

	assertDiagnostic(t, result, "overlapping_write", "left_writer", path.String())
}

func assertDiagnostic(t *testing.T, diagnostics []core.ContractDiagnostic, kind, nodeID, path string) {
	t.Helper()
	for _, diagnostic := range diagnostics {
		if diagnostic.Kind == kind && diagnostic.NodeID == nodeID && diagnostic.Path == path {
			return
		}
	}
	t.Fatalf("diagnostic kind=%q node=%q path=%q not found in %#v", kind, nodeID, path, diagnostics)
}
