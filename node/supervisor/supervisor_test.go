package supervisor

import (
	"context"
	"fmt"
	"strings"
	"testing"

	supervisorcap "github.com/dengzii/weaveflow/capability/supervisor"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/state"
)

func TestValidateFinishRequiresLatestWorkerMarkers(t *testing.T) {
	target := NewNode(core.WithID("sequential"))
	target.FinishWorker = "integrator"
	target.FinishMarkers = []string{"## tiny.go", "go test ./..."}

	if err := target.validateFinish(nil); err == nil || !strings.Contains(err.Error(), "before worker") {
		t.Fatalf("validateFinish(nil) error = %v", err)
	}
	history := []supervisorcap.Turn{{WorkerID: "integrator", Result: "## tiny.go"}}
	if err := target.validateFinish(history); err == nil || !strings.Contains(err.Error(), "go test ./...") {
		t.Fatalf("validateFinish(incomplete) error = %v", err)
	}
	history = append(history, supervisorcap.Turn{WorkerID: "integrator", Result: "## tiny.go\n`go test ./...`"})
	if err := target.validateFinish(history); err != nil {
		t.Fatalf("validateFinish(complete) error = %v", err)
	}
}

func TestSequentialSupervisorRoutesWithoutModel(t *testing.T) {
	target := NewNode(core.WithID("sequential_execute"))
	target.RoutingStrategy = SupervisorRoutingSequential
	target.Members = []Member{{ID: "design", Description: "define the language"}}
	result, err := core.ExecuteNode(context.Background(), state.FromShared(map[string]any{
		"request": map[string]any{"input": "build a language"},
	}), target)
	if err != nil {
		t.Fatalf("ExecuteNode() error = %v", err)
	}
	route, ok := state.ReadPath(result.State, target.SupervisorPath.MustChild(SupervisorFieldRoute).String())
	if !ok || route != "design" {
		t.Fatalf("route = %#v, found = %v", route, ok)
	}

	target.MaxTurns = 0
	target.Members = make([]Member, defaultSupervisorMaxTurns+1)
	for index := range target.Members {
		target.Members[index] = Member{ID: fmt.Sprintf("worker-%d", index), Description: "work"}
	}
	if err := target.Validate(); err == nil || !strings.Contains(err.Error(), "max_turns") {
		t.Fatalf("Validate() undersized max_turns error = %v", err)
	}
}

func TestValidateFinishCanCombineWorkerResults(t *testing.T) {
	target := NewNode()
	target.FinishWorker = "*"
	target.FinishMarkers = []string{"END_SOURCE", "END_TESTS"}
	history := []supervisorcap.Turn{
		{WorkerID: "integrator", Result: "END_SOURCE"},
		{WorkerID: "packager", Result: "END_TESTS"},
	}
	if err := target.validateFinish(history); err != nil {
		t.Fatalf("validateFinish(combined) error = %v", err)
	}
}

func TestSelectSequentialRouteUsesMemberOrder(t *testing.T) {
	target := NewNode(core.WithID("sequential"))
	target.RoutingStrategy = SupervisorRoutingSequential
	target.Members = []Member{
		{ID: "design", Description: "define the language"},
		{ID: "package", Description: "verify the package"},
	}
	if err := target.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	first, err := target.selectSequentialRoute(0)
	if err != nil || first.NextWorker != "design" || first.Task != "define the language" {
		t.Fatalf("first route = %#v, error = %v", first, err)
	}
	second, err := target.selectSequentialRoute(1)
	if err != nil || second.NextWorker != "package" || second.Task != "verify the package" {
		t.Fatalf("second route = %#v, error = %v", second, err)
	}
	finish, err := target.selectSequentialRoute(2)
	if err != nil || finish.NextWorker != SupervisorRouteFinish || finish.Task != "" {
		t.Fatalf("finish route = %#v, error = %v", finish, err)
	}
	if target.GraphNodeSpec().Config["routing_strategy"] != SupervisorRoutingSequential {
		t.Fatalf("graph config = %#v", target.GraphNodeSpec().Config)
	}
}
