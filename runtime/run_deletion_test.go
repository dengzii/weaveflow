package runtime

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type recordingRunDeleter struct {
	name  string
	calls *[]string
	err   error
}

func (store recordingRunDeleter) DeleteRun(_ context.Context, runID string) error {
	*store.calls = append(*store.calls, store.name+":"+runID)
	return store.err
}

func TestRunDeletionCoordinatorDeletesExecutionLast(t *testing.T) {
	var calls []string
	coordinator := NewRunDeletionCoordinator(
		recordingRunDeleter{name: "execution", calls: &calls},
		recordingRunDeleter{name: "checkpoints", calls: &calls},
		recordingRunDeleter{name: "events", calls: &calls},
		recordingRunDeleter{name: "artifacts", calls: &calls},
	)
	if err := coordinator.DeleteRun(context.Background(), " run-1 "); err != nil {
		t.Fatal(err)
	}
	want := []string{"checkpoints:run-1", "artifacts:run-1", "events:run-1", "execution:run-1"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("deletion calls = %#v, want %#v", calls, want)
	}
}

func TestRunDeletionCoordinatorStopsBeforeExecutionOnDependencyFailure(t *testing.T) {
	var calls []string
	failure := errors.New("artifact deletion failed")
	coordinator := NewRunDeletionCoordinator(
		recordingRunDeleter{name: "execution", calls: &calls},
		recordingRunDeleter{name: "checkpoints", calls: &calls},
		nil,
		recordingRunDeleter{name: "artifacts", calls: &calls, err: failure},
	)
	err := coordinator.DeleteRun(context.Background(), "run-1")
	if !errors.Is(err, failure) {
		t.Fatalf("DeleteRun() error = %v", err)
	}
	want := []string{"checkpoints:run-1", "artifacts:run-1"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("deletion calls = %#v, want %#v", calls, want)
	}
}
