package runtime

import (
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/state"
)

func TestGraphScheduleRoundTripRequiresCurrentVersion(t *testing.T) {
	current := state.NewState()
	want := GraphSchedule{
		CurrentTasks:      []GraphTask{{TaskID: "current", NodeID: "current"}},
		NextTasks:         []GraphTask{{TaskID: "next", NodeID: "next"}},
		PendingFanInTasks: []GraphTask{{TaskID: "pending", NodeID: "pending", Failure: &FailureContext{SourceNodeID: "failed"}}},
	}
	if err := StoreGraphSchedule(current, want); err != nil {
		t.Fatalf("StoreGraphSchedule() error = %v", err)
	}
	got, ok, err := LoadGraphSchedule(current)
	if err != nil {
		t.Fatalf("LoadGraphSchedule() error = %v", err)
	}
	if !ok || len(got.CurrentTasks) != 1 || len(got.NextTasks) != 1 || len(got.PendingFanInTasks) != 1 || got.PendingFanInTasks[0].Failure == nil {
		t.Fatalf("LoadGraphSchedule() = %#v, ok=%v", got, ok)
	}
}

func TestGraphScheduleRejectsLegacyAndMalformedMetadata(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*state.State)
		want    string
	}{
		{
			name: "legacy pending nodes",
			prepare: func(current *state.State) {
				_ = state.SetPath(current, graphSchedulerLegacyPendingPath.String(), []string{"join"})
			},
			want: "pending_fan_in_nodes",
		},
		{
			name: "missing version",
			prepare: func(current *state.State) {
				_ = state.SetPath(current, graphSchedulerCurrentPath.String(), []GraphTask{{TaskID: "task", NodeID: "node"}})
			},
			want: "version",
		},
		{
			name: "budget only missing version",
			prepare: func(current *state.State) {
				_ = state.SetPath(current, graphSchedulerStepsPath.String(), int64(1))
			},
			want: "version",
		},
		{
			name: "duplicate task ID",
			prepare: func(current *state.State) {
				_ = state.SetPath(current, graphSchedulerVersionPath.String(), graphSchedulerVersion)
				_ = state.SetPath(current, graphSchedulerCurrentPath.String(), []GraphTask{{TaskID: "task", NodeID: "first"}, {TaskID: "task", NodeID: "second"}})
			},
			want: "duplicate task ID",
		},
		{
			name: "duplicate pending node ID",
			prepare: func(current *state.State) {
				_ = state.SetPath(current, graphSchedulerVersionPath.String(), graphSchedulerVersion)
				_ = state.SetPath(current, graphSchedulerPendingTasksPath.String(), []GraphTask{{TaskID: "first", NodeID: "join"}, {TaskID: "second", NodeID: "join"}})
			},
			want: "duplicate node ID",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := state.NewState()
			test.prepare(current)
			_, ok, err := LoadGraphSchedule(current)
			if !ok || err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadGraphSchedule() ok=%v error=%v, want %q", ok, err, test.want)
			}
		})
	}
}

func TestGraphExecutionBudgetRejectsMalformedMetadata(t *testing.T) {
	current := state.NewState()
	if err := state.SetPath(current, graphSchedulerVersionPath.String(), graphSchedulerVersion); err != nil {
		t.Fatal(err)
	}
	if err := state.SetPath(current, graphSchedulerStepsPath.String(), "invalid"); err != nil {
		t.Fatal(err)
	}
	if err := state.SetPath(current, graphSchedulerNodesPath.String(), int64(1)); err != nil {
		t.Fatal(err)
	}
	if err := state.SetPath(current, graphSchedulerElapsedPath.String(), int64(1)); err != nil {
		t.Fatal(err)
	}

	_, ok, err := LoadGraphExecutionBudget(current)
	if !ok || err == nil || !strings.Contains(err.Error(), "invalid type") {
		t.Fatalf("LoadGraphExecutionBudget() ok=%v error=%v", ok, err)
	}
}
