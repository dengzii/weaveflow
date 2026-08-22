package supervisor

import (
	"strings"
	"testing"

	supervisorcap "github.com/dengzii/weaveflow/capability/supervisor"
	"github.com/dengzii/weaveflow/core"
)

func TestTaskWithSupervisorContext(t *testing.T) {
	history := []supervisorcap.Turn{
		{Turn: 1, WorkerID: "design", Task: "design", Result: "grammar"},
		{Turn: 2, WorkerID: "runtime", Task: "runtime", Result: "checker"},
	}
	if got := taskWithSupervisorContext("objective", "current", history, 0); got != "current" {
		t.Fatalf("taskWithSupervisorContext() without history = %q", got)
	}
	got := taskWithSupervisorContext("objective", "current", history, 1)
	for _, want := range []string{"Overall objective:\nobjective", `"worker_id": "runtime"`, "Current delegated task:\ncurrent"} {
		if !strings.Contains(got, want) {
			t.Fatalf("taskWithSupervisorContext() = %q, want substring %q", got, want)
		}
	}
	if strings.Contains(got, `"worker_id": "design"`) {
		t.Fatalf("taskWithSupervisorContext() included history outside configured window: %q", got)
	}
}

func TestWorkerToolFinalAnswerConfig(t *testing.T) {
	target := NewWorkerNode(core.WithID("packager"))
	target.WorkerID = "packager"
	target.ToolIDs = []string{"go_validate"}
	target.RequireToolFinalAnswer = true
	if err := target.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if required, ok := target.GraphNodeSpec().Config["require_tool_final_answer"].(bool); !ok || !required {
		t.Fatalf("worker config = %#v", target.GraphNodeSpec().Config)
	}

	target.ToolIDs = nil
	if err := target.Validate(); err == nil || !strings.Contains(err.Error(), "requires at least one tool_id") {
		t.Fatalf("Validate() without tools error = %v", err)
	}
}
