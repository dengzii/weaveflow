package graph

import (
	"fmt"
	"testing"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/state"
)

var graphBenchmarkResult any

func BenchmarkGraphCompile(b *testing.B) {
	for _, nodeCount := range []int{10, 100, 500} {
		b.Run(fmt.Sprintf("Nodes_%d", nodeCount), func(b *testing.B) {
			workflow := benchmarkGraph(nodeCount)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				compiled, err := workflow.Compile()
				if err != nil {
					b.Fatalf("compile graph: %v", err)
				}
				graphBenchmarkResult = compiled
			}
		})
	}
}

func BenchmarkGraphDefinitionAndHash(b *testing.B) {
	for _, nodeCount := range []int{10, 100, 500} {
		workflow := benchmarkGraph(nodeCount)
		b.Run(fmt.Sprintf("Definition/Nodes_%d", nodeCount), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				definition, err := workflow.Definition()
				if err != nil {
					b.Fatalf("graph definition: %v", err)
				}
				graphBenchmarkResult = definition
			}
		})
		b.Run(fmt.Sprintf("SemanticHash/Nodes_%d", nodeCount), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				hash, err := workflow.SemanticHash()
				if err != nil {
					b.Fatalf("semantic graph hash: %v", err)
				}
				graphBenchmarkResult = hash
			}
		})
		b.Run(fmt.Sprintf("SnapshotHash/Nodes_%d", nodeCount), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				hash, err := workflow.SnapshotHash()
				if err != nil {
					b.Fatalf("snapshot graph hash: %v", err)
				}
				graphBenchmarkResult = hash
			}
		})
	}
}

func benchmarkGraph(nodeCount int) *Graph {
	workflow := NewGraph(nil)
	for index := 0; index < nodeCount; index++ {
		nodeID := fmt.Sprintf("node-%04d", index)
		target := &benchmarkGraphNode{Base: node.NewBase(node.Spec{ID: nodeID, Name: nodeID})}
		if err := workflow.AddNode(target); err != nil {
			panic(err)
		}
		if index > 0 {
			if err := workflow.AddEdge(fmt.Sprintf("node-%04d", index-1), nodeID); err != nil {
				panic(err)
			}
		}
	}
	if err := workflow.SetEntryPoint("node-0000"); err != nil {
		panic(err)
	}
	if err := workflow.SetFinishPoint(fmt.Sprintf("node-%04d", nodeCount-1)); err != nil {
		panic(err)
	}
	return workflow
}

type benchmarkGraphNode struct {
	node.Base
}

func (n *benchmarkGraphNode) Execute(core.Context, *state.Access) (core.NodeResult, error) {
	return core.Success(), nil
}

func (n *benchmarkGraphNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return node.NewGraphNodeSpec(n.Base, "benchmark_worker", nil)
}
