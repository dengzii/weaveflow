package worker

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	sqlitestore "github.com/dengzii/weaveflow/internal/runtimestore/sqlite"
	"github.com/dengzii/weaveflow/runtime"
)

func TestRunOneHeartbeatsLongRunningTask(t *testing.T) {
	store, err := sqlitestore.Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()
	now := time.Now().UTC()
	if _, err := store.Enqueue(context.Background(), runtime.Task{
		ID: "task-1", Kind: "test", Status: runtime.TaskStatusQueued,
		CreatedAt: now, UpdatedAt: now, AvailableAt: now,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	handler := HandlerFunc(func(ctx context.Context, task runtime.Task) (runtime.TaskResult, error) {
		select {
		case <-ctx.Done():
			return runtime.TaskResult{}, ctx.Err()
		case <-time.After(750 * time.Millisecond):
			return runtime.TaskResult{Payload: []byte(`{"ok":true}`)}, nil
		}
	})
	durableWorker, err := New(store, handler, Config{
		Identity: runtime.WorkerIdentity{ID: "worker-1"}, Kinds: []string{"test"},
		LeaseTTL: 500 * time.Millisecond, HeartbeatInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	processed, err := durableWorker.RunOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("RunOne() = %v, %v", processed, err)
	}
	task, err := store.GetTask(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if task.Status != runtime.TaskStatusCompleted || string(task.Payload) != `{"ok":true}` {
		t.Fatalf("completed task = %#v", task)
	}
	attempts, err := store.ListAttempts(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("ListAttempts() error = %v", err)
	}
	if len(attempts) != 1 || attempts[0].Status != runtime.AttemptStatusCompleted || !attempts[0].HeartbeatAt.After(attempts[0].StartedAt) {
		t.Fatalf("attempts = %#v", attempts)
	}
}
