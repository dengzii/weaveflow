package supervisor

import (
	"testing"

	supervisorcap "github.com/dengzii/weaveflow/capability/supervisor"
)

func TestLatestWorkerResult(t *testing.T) {
	history := []supervisorcap.Turn{
		{WorkerID: "integrator", Result: "first"},
		{WorkerID: "reviewer", Result: "review"},
		{WorkerID: "Integrator", Result: " final "},
	}
	if got := latestWorkerResult(history, "integrator"); got != "final" {
		t.Fatalf("latestWorkerResult() = %q, want final", got)
	}
	if got := latestWorkerResult(history, "missing"); got != "" {
		t.Fatalf("latestWorkerResult() missing = %q, want empty", got)
	}
}

func TestCombinedWorkerResults(t *testing.T) {
	history := []supervisorcap.Turn{{WorkerID: "source", Result: "code"}, {WorkerID: "tests", Result: "checks"}}
	got, err := combinedWorkerResults(history, []string{"source", "tests"})
	if err != nil || got != "code\n\nchecks" {
		t.Fatalf("combinedWorkerResults() = %q, %v", got, err)
	}
	if _, err := combinedWorkerResults(history, []string{"missing"}); err == nil {
		t.Fatal("combinedWorkerResults() missing worker error = nil")
	}
}
