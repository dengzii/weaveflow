package state

import (
	"fmt"
	"testing"
)

var snapshotBenchmarkResult any

func BenchmarkStateSnapshotCodec(b *testing.B) {
	codec := NewJSONStateCodec("")
	for _, entryCount := range []int{10, 100, 1_000} {
		current := benchmarkSnapshotState(entryCount)
		snapshot, err := SnapshotFromState(current)
		if err != nil {
			b.Fatalf("prepare snapshot: %v", err)
		}
		encoded, err := codec.Encode(snapshot)
		if err != nil {
			b.Fatalf("prepare encoded snapshot: %v", err)
		}
		b.Run(fmt.Sprintf("SnapshotFromState/Entries_%d", entryCount), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				value, err := SnapshotFromState(current)
				if err != nil {
					b.Fatalf("snapshot from state: %v", err)
				}
				snapshotBenchmarkResult = value
			}
		})
		b.Run(fmt.Sprintf("Encode/Entries_%d", entryCount), func(b *testing.B) {
			b.SetBytes(int64(len(encoded)))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				value, err := codec.Encode(snapshot)
				if err != nil {
					b.Fatalf("encode snapshot: %v", err)
				}
				snapshotBenchmarkResult = value
			}
		})
		b.Run(fmt.Sprintf("Decode/Entries_%d", entryCount), func(b *testing.B) {
			b.SetBytes(int64(len(encoded)))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				value, err := codec.Decode(encoded)
				if err != nil {
					b.Fatalf("decode snapshot: %v", err)
				}
				snapshotBenchmarkResult = value
			}
		})
	}
}

func BenchmarkStateDiffSnapshots(b *testing.B) {
	for _, entryCount := range []int{10, 100, 1_000} {
		before, after := benchmarkSnapshots(entryCount)
		b.Run(fmt.Sprintf("Entries_%d", entryCount), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				changes, err := DiffSnapshots(before, after)
				if err != nil {
					b.Fatalf("diff snapshots: %v", err)
				}
				snapshotBenchmarkResult = changes
			}
		})
	}
}

func benchmarkSnapshotState(entryCount int) *State {
	access := NewEditingAccess(NewState())
	for index := 0; index < entryCount; index++ {
		path := Shared("records", fmt.Sprintf("record-%04d", index))
		if err := access.SetAny(path, map[string]any{
			"index":  index,
			"status": "ready",
			"value":  fmt.Sprintf("payload-%04d", index),
		}); err != nil {
			panic(err)
		}
	}
	return access.State()
}

func benchmarkSnapshots(entryCount int) (Snapshot, Snapshot) {
	before, err := SnapshotFromState(benchmarkSnapshotState(entryCount))
	if err != nil {
		panic(err)
	}
	afterState := benchmarkSnapshotState(entryCount)
	if err := SetPath(afterState, "shared.records.record-0000.status", "updated"); err != nil {
		panic(err)
	}
	if err := SetPath(afterState, fmt.Sprintf("shared.records.record-%04d", entryCount), map[string]any{"index": entryCount, "status": "new"}); err != nil {
		panic(err)
	}
	after, err := SnapshotFromState(afterState)
	if err != nil {
		panic(err)
	}
	return before, after
}
