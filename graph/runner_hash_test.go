package graph

import (
	"context"
	"testing"

	"github.com/dengzii/weaveflow/dsl"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

func TestNewGraphRunnerPopulatesGraphHashes(t *testing.T) {
	g := NewGraph()
	mustAddNode(t, g, "input", func(context.Context, *state.Access) error {
		return nil
	})
	if err := g.SetEntryPoint("input"); err != nil {
		t.Fatalf("set entry point: %v", err)
	}
	if err := g.SetFinishPoint("input"); err != nil {
		t.Fatalf("set finish point: %v", err)
	}

	def, err := g.Definition()
	if err != nil {
		t.Fatalf("graph definition: %v", err)
	}
	graphHash, err := dsl.SemanticGraphHash(def)
	if err != nil {
		t.Fatalf("semantic graph hash: %v", err)
	}
	graphSnapshotHash, err := dsl.SnapshotGraphHash(def)
	if err != nil {
		t.Fatalf("snapshot graph hash: %v", err)
	}

	dir := t.TempDir()
	runner := NewGraphRunner(
		g,
		fruntime.NewFileExecutionStore(dir),
		fruntime.NewFileCheckpointStore(dir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)
	if runner.GraphHash != graphHash {
		t.Fatalf("runner graph hash = %q, want %q", runner.GraphHash, graphHash)
	}
	if runner.GraphSnapshotHash != graphSnapshotHash {
		t.Fatalf("runner graph snapshot hash = %q, want %q", runner.GraphSnapshotHash, graphSnapshotHash)
	}

	run, _, err := runner.Start(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("start runner: %v", err)
	}
	if run.GraphHash != graphHash {
		t.Fatalf("run graph hash = %q, want %q", run.GraphHash, graphHash)
	}
	if run.GraphSnapshotHash != graphSnapshotHash {
		t.Fatalf("run graph snapshot hash = %q, want %q", run.GraphSnapshotHash, graphSnapshotHash)
	}
}
