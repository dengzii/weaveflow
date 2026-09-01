package dsl

import (
	"fmt"
	"testing"
)

var (
	graphBenchmarkBytes      []byte
	graphBenchmarkDefinition GraphDefinition
	graphBenchmarkHash       string
)

func BenchmarkGraphDefinitionSerialize(b *testing.B) {
	for _, nodeCount := range []int{10, 100, 1_000} {
		b.Run(fmt.Sprintf("Nodes_%d", nodeCount), func(b *testing.B) {
			definition, _ := benchmarkGraphFixture(nodeCount)
			encoded, err := definition.Serialize()
			if err != nil {
				b.Fatalf("prepare serialized graph: %v", err)
			}
			b.SetBytes(int64(len(encoded)))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				encoded, err = definition.Serialize()
				if err != nil {
					b.Fatalf("serialize graph definition: %v", err)
				}
				graphBenchmarkBytes = encoded
			}
		})
	}
}

func BenchmarkGraphDefinitionDeserialize(b *testing.B) {
	for _, nodeCount := range []int{10, 100, 1_000} {
		b.Run(fmt.Sprintf("Nodes_%d", nodeCount), func(b *testing.B) {
			definition, _ := benchmarkGraphFixture(nodeCount)
			encoded, err := definition.Serialize()
			if err != nil {
				b.Fatalf("prepare serialized graph: %v", err)
			}
			b.SetBytes(int64(len(encoded)))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				definition, err = DeserializeGraphDefinition(encoded)
				if err != nil {
					b.Fatalf("deserialize graph definition: %v", err)
				}
				graphBenchmarkDefinition = definition
			}
		})
	}
}

func BenchmarkGraphHash(b *testing.B) {
	for _, nodeCount := range []int{10, 100, 1_000} {
		definition, bindings := benchmarkGraphFixture(nodeCount)
		encoded, err := definition.Serialize()
		if err != nil {
			b.Fatalf("prepare serialized graph: %v", err)
		}
		b.Run(fmt.Sprintf("Semantic/Nodes_%d", nodeCount), func(b *testing.B) {
			benchmarkGraphHashOperation(b, int64(len(encoded)), func() (string, error) {
				return SemanticGraphHash(definition)
			})
		})
		b.Run(fmt.Sprintf("SemanticWithBindings/Nodes_%d", nodeCount), func(b *testing.B) {
			benchmarkGraphHashOperation(b, int64(len(encoded)), func() (string, error) {
				return SemanticGraphHashWithStateBindings(definition, bindings)
			})
		})
		b.Run(fmt.Sprintf("Snapshot/Nodes_%d", nodeCount), func(b *testing.B) {
			benchmarkGraphHashOperation(b, int64(len(encoded)), func() (string, error) {
				return SnapshotGraphHash(definition)
			})
		})
	}
}

func benchmarkGraphHashOperation(b *testing.B, graphBytes int64, hash func() (string, error)) {
	b.Helper()
	value, err := hash()
	if err != nil {
		b.Fatalf("prepare graph hash: %v", err)
	}
	graphBenchmarkHash = value
	b.SetBytes(graphBytes)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		value, err = hash()
		if err != nil {
			b.Fatalf("hash graph definition: %v", err)
		}
		graphBenchmarkHash = value
	}
}

func benchmarkGraphFixture(nodeCount int) (GraphDefinition, []StateBindingSemantic) {
	if nodeCount < 1 {
		nodeCount = 1
	}
	nodes := make([]GraphNodeSpec, nodeCount)
	edges := make([]GraphEdgeSpec, 0, nodeCount-1)
	bindings := make([]StateBindingSemantic, nodeCount)
	for index := range nodes {
		nodeID := fmt.Sprintf("node-%04d", index)
		nodes[index] = GraphNodeSpec{
			ID:          nodeID,
			Name:        fmt.Sprintf("Worker %d", index),
			Type:        "benchmark_worker",
			Description: "Processes one deterministic benchmark step.",
			Config: map[string]any{
				"index":  index,
				"labels": []any{"benchmark", "worker"},
				"options": map[string]any{
					"enabled": true,
					"limit":   100,
				},
			},
			State: map[string]StateBinding{
				"input":  {Path: fmt.Sprintf("shared.pipeline.%s.input", nodeID)},
				"output": {Path: fmt.Sprintf("shared.pipeline.%s.output", nodeID)},
			},
		}
		bindings[index] = StateBindingSemantic{
			ComponentType: "node",
			ComponentID:   nodeID,
			Port:          "output",
			Path:          fmt.Sprintf("shared.pipeline.%s.output", nodeID),
			Capability:    "benchmark.result.v1",
			Contract: []StateContractSemanticField{
				{Path: fmt.Sprintf("shared.pipeline.%s.output.status", nodeID), Mode: StateAccessWrite, MergeStrategy: StateMergeReplace, Type: "string"},
				{Path: fmt.Sprintf("shared.pipeline.%s.output.items", nodeID), Mode: StateAccessWrite, MergeStrategy: StateMergeAppend, Type: "array"},
			},
		}
		if index > 0 {
			edges = append(edges, GraphEdgeSpec{
				From: fmt.Sprintf("node-%04d", index-1),
				To:   nodeID,
			})
		}
	}
	return GraphDefinition{
		Version:      GraphDefinitionVersion,
		Name:         "benchmark-graph",
		Description:  "Graph fixture used to measure serialization and hash costs.",
		StateModules: []StateModuleRef{{Name: "benchmark.protocols", Version: "1"}},
		EntryPoint:   nodes[0].ID,
		FinishPoint:  nodes[len(nodes)-1].ID,
		Nodes:        nodes,
		Edges:        edges,
		Metadata: map[string]any{
			"web": map[string]any{
				"layout": "horizontal",
				"zoom":   1.25,
			},
		},
	}, bindings
}
