package dsl

import "testing"

func TestSemanticGraphHashIgnoresMetadataAndNodeOrder(t *testing.T) {
	base := GraphDefinition{
		Version:     GraphDefinitionVersion,
		Name:        "hash-graph",
		StateSchema: CommonStateSchemaID,
		EntryPoint:  "start",
		FinishPoint: "done",
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
		Version:     GraphDefinitionVersion,
		Name:        "hash-graph",
		StateSchema: CommonStateSchemaID,
		EntryPoint:  "start",
		FinishPoint: "start",
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
		Version:     GraphDefinitionVersion,
		Name:        "hash-graph",
		StateSchema: CommonStateSchemaID,
		EntryPoint:  "router",
		FinishPoint: "router",
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
