package dsl

import "testing"

func TestSemanticGraphHashIgnoresMetadataAndNodeOrder(t *testing.T) {
	base := GraphDefinition{
		Version:      GraphDefinitionVersion,
		Name:         "hash-graph",
		StateModules: []StateModuleRef{{Name: "test", Version: "1"}},
		EntryPoint:   "start",
		FinishPoint:  "done",
		Nodes: []GraphNodeSpec{
			{
				ID:   "start",
				Name: "Start",
				Type: "input",
				Config: map[string]any{
					"b": 2,
					"a": 1,
				},
			},
			{
				ID:   "done",
				Name: "Done",
				Type: "output",
			},
		},
		Edges: []GraphEdgeSpec{
			{From: "start", To: "done"},
		},
		Metadata: map[string]any{
			"web": map[string]any{
				"positions": map[string]any{
					"start": map[string]any{"x": 10, "y": 20},
				},
			},
		},
	}

	reordered := base
	reordered.Nodes = []GraphNodeSpec{base.Nodes[1], base.Nodes[0]}
	reordered.Metadata = map[string]any{
		"web": map[string]any{
			"positions": map[string]any{
				"start": map[string]any{"x": 999, "y": 888},
				"done":  map[string]any{"x": 777, "y": 666},
			},
		},
	}

	left, err := SemanticGraphHash(base)
	if err != nil {
		t.Fatalf("semantic hash base: %v", err)
	}
	right, err := SemanticGraphHash(reordered)
	if err != nil {
		t.Fatalf("semantic hash reordered: %v", err)
	}
	if left != right {
		t.Fatalf("semantic hash changed for metadata/node order: %q != %q", left, right)
	}
}

func TestSnapshotGraphHashIncludesMetadata(t *testing.T) {
	base := GraphDefinition{
		Version:      GraphDefinitionVersion,
		Name:         "hash-graph",
		StateModules: []StateModuleRef{{Name: "test", Version: "1"}},
		EntryPoint:   "start",
		FinishPoint:  "start",
		Nodes: []GraphNodeSpec{
			{ID: "start", Name: "Start", Type: "input"},
		},
		Metadata: map[string]any{
			"web": map[string]any{"positions": map[string]any{"start": map[string]any{"x": 10, "y": 20}}},
		},
	}
	changed := base
	changed.Metadata = map[string]any{
		"web": map[string]any{"positions": map[string]any{"start": map[string]any{"x": 30, "y": 40}}},
	}

	left, err := SnapshotGraphHash(base)
	if err != nil {
		t.Fatalf("snapshot hash base: %v", err)
	}
	right, err := SnapshotGraphHash(changed)
	if err != nil {
		t.Fatalf("snapshot hash changed: %v", err)
	}
	if left == right {
		t.Fatalf("snapshot hash did not change after metadata change: %q", left)
	}
}

func TestSemanticGraphHashPreservesEdgeOrder(t *testing.T) {
	base := GraphDefinition{
		Version:      GraphDefinitionVersion,
		Name:         "hash-graph",
		StateModules: []StateModuleRef{{Name: "test", Version: "1"}},
		EntryPoint:   "router",
		FinishPoint:  "router",
		Nodes: []GraphNodeSpec{
			{ID: "router", Name: "Router", Type: "router"},
			{ID: "left", Name: "Left", Type: "worker"},
			{ID: "right", Name: "Right", Type: "worker"},
		},
		Edges: []GraphEdgeSpec{
			{From: "router", To: "left"},
			{From: "router", To: "right"},
		},
	}
	reordered := base
	reordered.Edges = []GraphEdgeSpec{base.Edges[1], base.Edges[0]}

	left, err := SemanticGraphHash(base)
	if err != nil {
		t.Fatalf("semantic hash base: %v", err)
	}
	right, err := SemanticGraphHash(reordered)
	if err != nil {
		t.Fatalf("semantic hash reordered: %v", err)
	}
	if left == right {
		t.Fatalf("semantic hash did not change after edge order change: %q", left)
	}
}

func TestSemanticGraphHashCanonicalizesModulesAndIncludesVersions(t *testing.T) {
	t.Parallel()
	base := GraphDefinition{
		Version: GraphDefinitionVersion,
		StateModules: []StateModuleRef{
			{Name: "example.protocols", Version: "1"},
			{Name: "example.memory", Version: "2"},
		},
		Nodes: []GraphNodeSpec{{
			ID: "node", Type: "node",
			State: map[string]StateBinding{"input": {Path: "shared.input"}},
		}},
	}
	reordered := base
	reordered.StateModules = []StateModuleRef{base.StateModules[1], base.StateModules[0]}
	changedVersion := base
	changedVersion.StateModules = []StateModuleRef{
		{Name: "example.protocols", Version: "1"},
		{Name: "example.memory", Version: "3"},
	}
	changedBinding := base
	changedBinding.Nodes = []GraphNodeSpec{{
		ID: "node", Type: "node",
		State: map[string]StateBinding{"input": {Path: "shared.other"}},
	}}
	spacedBinding := base
	spacedBinding.Nodes = []GraphNodeSpec{{
		ID: "node", Type: "node",
		State: map[string]StateBinding{"input": {Path: "  shared.input  "}},
	}}
	changedReducer := base
	changedReducer.Nodes = []GraphNodeSpec{{
		ID: "node", Type: "node",
		State: map[string]StateBinding{"input": {Path: "shared.input", Reducer: "sum.v1"}},
	}}

	baseHash, err := SemanticGraphHash(base)
	if err != nil {
		t.Fatalf("semantic hash base: %v", err)
	}
	reorderedHash, err := SemanticGraphHash(reordered)
	if err != nil {
		t.Fatalf("semantic hash reordered modules: %v", err)
	}
	if reorderedHash != baseHash {
		t.Fatalf("module order changed semantic hash: %q != %q", reorderedHash, baseHash)
	}
	versionHash, err := SemanticGraphHash(changedVersion)
	if err != nil {
		t.Fatalf("semantic hash changed version: %v", err)
	}
	if versionHash == baseHash {
		t.Fatal("module version did not change semantic hash")
	}
	bindingHash, err := SemanticGraphHash(changedBinding)
	if err != nil {
		t.Fatalf("semantic hash changed binding: %v", err)
	}
	if bindingHash == baseHash {
		t.Fatal("state binding did not change semantic hash")
	}
	spacedHash, err := SemanticGraphHash(spacedBinding)
	if err != nil {
		t.Fatalf("semantic hash spaced binding: %v", err)
	}
	if spacedHash != baseHash {
		t.Fatalf("binding whitespace changed semantic hash: %q != %q", spacedHash, baseHash)
	}
	reducerHash, err := SemanticGraphHash(changedReducer)
	if err != nil {
		t.Fatalf("semantic hash changed reducer: %v", err)
	}
	if reducerHash == baseHash {
		t.Fatal("state binding reducer did not change semantic hash")
	}
}

func TestSemanticGraphHashIncludesResolvedCapabilityAndContract(t *testing.T) {
	t.Parallel()
	def := GraphDefinition{
		Version:      GraphDefinitionVersion,
		StateModules: []StateModuleRef{{Name: "test", Version: "1"}},
		Nodes: []GraphNodeSpec{{
			ID: "node", Type: "consumer", State: map[string]StateBinding{"root": {Path: "shared.thread"}},
		}},
	}
	base := []StateBindingSemantic{{
		ComponentType: "node", ComponentID: "node", Port: "root", Path: "shared.thread", Capability: "test.conversation.v1",
		Contract: []StateContractSemanticField{{Path: "shared.thread.messages", Mode: StateAccessReadWrite, MergeStrategy: StateMergeReplace, Type: "array"}},
	}}
	reordered := append([]StateBindingSemantic(nil), base...)
	reordered[0].Contract = []StateContractSemanticField{base[0].Contract[0]}
	changedCapability := append([]StateBindingSemantic(nil), base...)
	changedCapability[0].Capability = "test.conversation.v2"
	changedContract := append([]StateBindingSemantic(nil), base...)
	changedContract[0].Contract = append([]StateContractSemanticField(nil), base[0].Contract...)
	changedContract[0].Contract[0].MergeStrategy = StateMergeAppend
	changedReducer := append([]StateBindingSemantic(nil), base...)
	changedReducer[0].Contract = append([]StateContractSemanticField(nil), base[0].Contract...)
	changedReducer[0].Contract[0].Reducer = "messages.v1"

	baseHash, err := SemanticGraphHashWithStateBindings(def, base)
	if err != nil {
		t.Fatalf("base hash: %v", err)
	}
	reorderedHash, _ := SemanticGraphHashWithStateBindings(def, reordered)
	if reorderedHash != baseHash {
		t.Fatalf("equivalent bindings changed hash: %q != %q", reorderedHash, baseHash)
	}
	capabilityHash, _ := SemanticGraphHashWithStateBindings(def, changedCapability)
	if capabilityHash == baseHash {
		t.Fatal("capability major did not change semantic hash")
	}
	contractHash, _ := SemanticGraphHashWithStateBindings(def, changedContract)
	if contractHash == baseHash {
		t.Fatal("resolved contract did not change semantic hash")
	}
	reducerHash, _ := SemanticGraphHashWithStateBindings(def, changedReducer)
	if reducerHash == baseHash {
		t.Fatal("resolved reducer did not change semantic hash")
	}
}
