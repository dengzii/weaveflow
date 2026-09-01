package state

import (
	"fmt"
	"testing"
)

var parallelMergeBenchmarkResult *State

func BenchmarkMergeParallelPatches(b *testing.B) {
	for _, branchCount := range []int{8, 64, 256} {
		b.Run(fmt.Sprintf("DisjointSet/Branches_%d", branchCount), func(b *testing.B) {
			base := FromShared(map[string]any{
				"metadata": map[string]any{"run_id": "benchmark-run", "attempt": 1},
			})
			branches := benchmarkDisjointSetBranches(branchCount)
			benchmarkParallelMerge(b, base, branches)
		})
		b.Run(fmt.Sprintf("SharedAppend/Branches_%d", branchCount), func(b *testing.B) {
			base := FromShared(map[string]any{"results": []int{-1}})
			branches := benchmarkSharedAppendBranches(branchCount)
			benchmarkParallelMerge(b, base, branches)
		})
	}
}

func benchmarkParallelMerge(b *testing.B, base *State, branches []BranchPatch) {
	b.Helper()
	merged, err := MergeParallelPatches(base, branches, ParallelMergeOptions{})
	if err != nil {
		b.Fatalf("prepare parallel merge: %v", err)
	}
	parallelMergeBenchmarkResult = merged
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		merged, err = MergeParallelPatches(base, branches, ParallelMergeOptions{})
		if err != nil {
			b.Fatalf("merge parallel patches: %v", err)
		}
		parallelMergeBenchmarkResult = merged
	}
	b.ReportMetric(float64(len(branches)), "branches/op")
}

func benchmarkDisjointSetBranches(branchCount int) []BranchPatch {
	branches := make([]BranchPatch, branchCount)
	for index := range branches {
		nodeID := fmt.Sprintf("worker-%04d", index)
		branches[index] = BranchPatch{
			TaskID: nodeID,
			NodeID: nodeID,
			Order:  branchCount - index,
			Patch: NewPatch(PatchOp{
				Kind: OpSet,
				Path: Shared("results", nodeID),
				Value: map[string]any{
					"index":  index,
					"status": "completed",
				},
			}),
		}
	}
	return branches
}

func benchmarkSharedAppendBranches(branchCount int) []BranchPatch {
	branches := make([]BranchPatch, branchCount)
	for index := range branches {
		nodeID := fmt.Sprintf("worker-%04d", index)
		branches[index] = BranchPatch{
			TaskID: nodeID,
			NodeID: nodeID,
			Order:  branchCount - index,
			Patch: NewPatch(PatchOp{
				Kind:  OpAppend,
				Path:  Shared("results"),
				Value: index,
			}),
		}
	}
	return branches
}
