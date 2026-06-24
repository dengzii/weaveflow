package dsl

import (
	"strings"
	"testing"
)

func TestGraphDefinitionRejectsDuplicateEdges(t *testing.T) {
	t.Parallel()

	def := GraphDefinition{
		Version:     GraphDefinitionVersion,
		EntryPoint:  "router",
		FinishPoint: "a",
		Nodes: []GraphNodeSpec{
			{ID: "router", Type: "router"},
			{ID: "a", Type: "worker"},
		},
		Edges: []GraphEdgeSpec{
			{From: "router", To: "a"},
			{From: "router", To: "a"},
		},
	}

	err := def.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("expected duplicate edge validation error, got %v", err)
	}
}

func TestGraphDefinitionAllowsFanOutEdges(t *testing.T) {
	t.Parallel()

	def := GraphDefinition{
		Version:     GraphDefinitionVersion,
		EntryPoint:  "router",
		FinishPoint: "b",
		Nodes: []GraphNodeSpec{
			{ID: "router", Type: "router"},
			{ID: "a", Type: "worker"},
			{ID: "b", Type: "worker"},
		},
		Edges: []GraphEdgeSpec{
			{From: "router", To: "a"},
			{From: "router", To: "b"},
		},
	}

	if err := def.Validate(); err != nil {
		t.Fatalf("expected fan-out edges to validate: %v", err)
	}
}
