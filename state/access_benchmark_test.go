package state

import (
	"fmt"
	"testing"
)

var (
	stateAccessBenchmarkValue any
	stateAccessBenchmarkState *State
)

func BenchmarkStateAccessRead(b *testing.B) {
	base := benchmarkHotState()
	for _, path := range []struct {
		name string
		path Path
	}{
		{name: "Scalar", path: Shared("counters", "requests")},
		{name: "NestedObject", path: Shared("session", "metadata")},
		{name: "DeepValue", path: Scope("agent", "conversation", "metadata", "tenant")},
	} {
		b.Run(path.name, func(b *testing.B) {
			access := NewAccess(base)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				value, ok := access.ReadAny(path.path)
				if !ok {
					b.Fatal("hot state path is missing")
				}
				stateAccessBenchmarkValue = value
			}
		})
	}
}

func BenchmarkStateAccessReadParallel(b *testing.B) {
	base := benchmarkHotState()
	path := Scope("agent", "conversation", "metadata")
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		access := NewAccess(base)
		for pb.Next() {
			value, ok := access.ReadAny(path)
			if !ok {
				b.Fatal("hot state path is missing")
			}
			if value == nil {
				b.Fatal("hot state value is nil")
			}
		}
	})
}

func BenchmarkStateAccessWrite(b *testing.B) {
	base := benchmarkHotState()
	for _, write := range []struct {
		name  string
		path  Path
		value any
	}{
		{name: "Scalar", path: Shared("counters", "requests"), value: 42},
		{name: "NestedObject", path: Shared("session", "metadata"), value: map[string]any{"region": "ap-southeast", "tier": "gold"}},
	} {
		b.Run(write.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				access := NewEditingAccess(base)
				if err := access.SetAny(write.path, write.value); err != nil {
					b.Fatal(err)
				}
				stateAccessBenchmarkState = access.State()
			}
		})
	}
}

func BenchmarkStateAccessReadWriteTransaction(b *testing.B) {
	base := benchmarkHotState()
	path := Shared("counters", "requests")
	b.ReportAllocs()
	for b.Loop() {
		access := NewEditingAccess(base)
		value, ok := access.ReadAny(path)
		if !ok {
			b.Fatal("hot state path is missing")
		}
		count, ok := value.(int)
		if !ok {
			b.Fatalf("counter type = %T", value)
		}
		if err := access.SetAny(path, count+1); err != nil {
			b.Fatal(err)
		}
		stateAccessBenchmarkState = access.State()
	}
}

func BenchmarkStateAccessReadWriteBatch(b *testing.B) {
	base := benchmarkHotState()
	path := Shared("counters", "requests")
	for _, batchSize := range []int{1, 16, 256} {
		b.Run(fmt.Sprintf("Operations_%d", batchSize), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				access := NewEditingAccess(base)
				for index := 0; index < batchSize; index++ {
					value, ok := access.ReadAny(path)
					if !ok {
						b.Fatal("hot state path is missing")
					}
					count, ok := value.(int)
					if !ok {
						b.Fatalf("counter type = %T", value)
					}
					if err := access.SetAny(path, count+1); err != nil {
						b.Fatal(err)
					}
				}
				stateAccessBenchmarkState = access.State()
			}
			b.ReportMetric(float64(batchSize), "operations/op")
		})
	}
}

func benchmarkHotState() *State {
	return FromMap(map[string]any{
		SectionShared: map[string]any{
			"counters": map[string]any{
				"requests": 41,
				"errors":   3,
			},
			"session": map[string]any{
				"id": "session-benchmark",
				"metadata": map[string]any{
					"region": "ap-southeast",
					"tenant": "tenant-001",
					"labels": map[string]any{
						"environment": "benchmark",
						"component":   "state",
					},
				},
			},
		},
		SectionScopes: map[string]any{
			"agent": map[string]any{
				"conversation": map[string]any{
					"metadata": map[string]any{
						"tenant": "tenant-001",
						"labels": map[string]any{
							"environment": "benchmark",
						},
					},
				},
			},
		},
	})
}
