package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	fruntime "github.com/dengzii/weaveflow/runtime"
)

func TestEnqueueWithCommitPersistsRunAndTaskAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.db")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	now := time.Unix(100, 0).UTC()
	run := testRun("run-1", now)
	task := fruntime.Task{ID: run.RunID, Kind: fruntime.TaskKindGraphRun, RunID: run.RunID, Status: fruntime.TaskStatusQueued, CreatedAt: now, UpdatedAt: now, AvailableAt: now}
	queued, result, err := store.EnqueueWithCommit(context.Background(), task, fruntime.Commit{
		TransactionID: "transaction-1",
		Run:           &fruntime.RunWrite{Mode: fruntime.RunWriteCreate, Run: run},
	})
	if err != nil {
		t.Fatalf("EnqueueWithCommit() error = %v", err)
	}
	if result.Outcome != fruntime.TransactionCommitted || queued.ID != task.ID {
		t.Fatalf("EnqueueWithCommit() = %#v, %#v", queued, result)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	defer func() { _ = reopened.Close() }()
	if persisted, err := reopened.GetRun(context.Background(), run.RunID); err != nil || persisted.RunID != run.RunID {
		t.Fatalf("GetRun() = %#v, %v", persisted, err)
	}
	if persisted, err := reopened.GetTask(context.Background(), task.ID); err != nil || persisted.Status != fruntime.TaskStatusQueued {
		t.Fatalf("GetTask() = %#v, %v", persisted, err)
	}
	if resolved, err := reopened.ResolveCommit(context.Background(), "transaction-1"); err != nil || resolved.Outcome != fruntime.TransactionCommitted {
		t.Fatalf("ResolveCommit() = %#v, %v", resolved, err)
	}
}

func TestConcurrentClaimHasSingleOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.db")
	first, err := Open(path)
	if err != nil {
		t.Fatalf("Open(first) error = %v", err)
	}
	defer func() { _ = first.Close() }()
	second, err := Open(path)
	if err != nil {
		t.Fatalf("Open(second) error = %v", err)
	}
	defer func() { _ = second.Close() }()
	now := time.Unix(200, 0).UTC()
	if _, err := first.Enqueue(context.Background(), fruntime.Task{ID: "task-1", Kind: "test", Status: fruntime.TaskStatusQueued, CreatedAt: now, UpdatedAt: now, AvailableAt: now}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	stores := []*Store{first, second}
	results := make(chan error, len(stores))
	var wait sync.WaitGroup
	for index, store := range stores {
		wait.Add(1)
		go func(index int, store *Store) {
			defer wait.Done()
			_, _, err := store.Claim(context.Background(), fruntime.WorkerIdentity{ID: "worker-" + string(rune('a'+index))}, fruntime.TaskClaimOptions{Kinds: []string{"test"}, Now: now, TTL: time.Minute})
			results <- err
		}(index, store)
	}
	wait.Wait()
	close(results)
	succeeded := 0
	notFound := 0
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, fruntime.ErrTaskNotFound):
			notFound++
		default:
			t.Fatalf("Claim() unexpected error = %v", err)
		}
	}
	if succeeded != 1 || notFound != 1 {
		t.Fatalf("claims succeeded = %d, not found = %d", succeeded, notFound)
	}
}

func TestExpiredLeaseTakeoverFencesOldOwner(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()
	now := time.Now().UTC()
	if _, err := store.Enqueue(context.Background(), fruntime.Task{ID: "task-1", Kind: "test", Status: fruntime.TaskStatusQueued, CreatedAt: now, UpdatedAt: now, AvailableAt: now}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	first, _, err := store.Claim(context.Background(), fruntime.WorkerIdentity{ID: "worker-1"}, fruntime.TaskClaimOptions{Now: now, TTL: time.Second})
	if err != nil {
		t.Fatalf("Claim(first) error = %v", err)
	}
	second, _, err := store.Claim(context.Background(), fruntime.WorkerIdentity{ID: "worker-2"}, fruntime.TaskClaimOptions{Now: now.Add(2 * time.Second), TTL: time.Minute})
	if err != nil {
		t.Fatalf("Claim(second) error = %v", err)
	}
	if first.Lease.Epoch+1 != second.Lease.Epoch || first.Lease.Token == second.Lease.Token {
		t.Fatalf("lease takeover = %#v -> %#v", first.Lease, second.Lease)
	}
	if _, err := store.Complete(context.Background(), *first.Lease, fruntime.TaskResult{}); !errors.Is(err, fruntime.ErrTaskLeaseLost) {
		t.Fatalf("old Complete() error = %v, want task lease lost", err)
	}
	completed, err := store.Complete(context.Background(), *second.Lease, fruntime.TaskResult{})
	if err != nil || completed.Status != fruntime.TaskStatusCompleted {
		t.Fatalf("new Complete() = %#v, %v", completed, err)
	}
	attempts, err := store.ListAttempts(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("ListAttempts() error = %v", err)
	}
	if len(attempts) != 2 || attempts[0].Status != fruntime.AttemptStatusAbandoned || attempts[1].Status != fruntime.AttemptStatusCompleted {
		t.Fatalf("attempts = %#v", attempts)
	}
}

func TestFailWithCommitRollsBackTaskAndRunTogether(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()
	now := time.Now().UTC()
	run := testRun("run-1", now)
	if _, err := store.Commit(context.Background(), fruntime.Commit{
		TransactionID: "create-run",
		Run:           &fruntime.RunWrite{Mode: fruntime.RunWriteCreate, Run: run},
	}); err != nil {
		t.Fatalf("Commit(create) error = %v", err)
	}
	if _, err := store.Enqueue(context.Background(), fruntime.Task{
		ID: "task-1", Kind: fruntime.TaskKindGraphNode, RunID: run.RunID,
		Status: fruntime.TaskStatusQueued, CreatedAt: now, UpdatedAt: now, AvailableAt: now,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	claimed, _, err := store.Claim(context.Background(), fruntime.WorkerIdentity{ID: "worker-1"}, fruntime.TaskClaimOptions{
		TaskID: "task-1", Now: now, TTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	persistedRun, err := store.GetRun(context.Background(), run.RunID)
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	persistedRun.Status = fruntime.RunStatusFailed
	persistedRun.ErrorMessage = "node failed"
	persistedRun.UpdatedAt = now.Add(time.Second)
	persistedRun.FinishedAt = &persistedRun.UpdatedAt
	failures := []fruntime.TaskFailureTransition{{Lease: *claimed.Lease, Failure: fruntime.TaskFailure{Message: "node failed"}}}
	if _, _, err := store.FailWithCommit(context.Background(), failures, fruntime.Commit{
		TransactionID: "fail-invalid",
		Run:           &fruntime.RunWrite{Mode: fruntime.RunWriteCreate, Run: persistedRun},
	}); err == nil {
		t.Fatal("FailWithCommit(invalid) error = nil")
	}
	stillRunning, err := store.GetTask(context.Background(), claimed.ID)
	if err != nil || stillRunning.Status != fruntime.TaskStatusRunning || stillRunning.Lease == nil {
		t.Fatalf("task after rollback = %#v, %v", stillRunning, err)
	}
	stillPending, err := store.GetRun(context.Background(), run.RunID)
	if err != nil || stillPending.Status != fruntime.RunStatusPending {
		t.Fatalf("run after rollback = %#v, %v", stillPending, err)
	}
	tasks, result, err := store.FailWithCommit(context.Background(), failures, fruntime.Commit{
		TransactionID: "fail-valid",
		Run:           &fruntime.RunWrite{Mode: fruntime.RunWriteUpdate, Run: persistedRun},
	})
	if err != nil {
		t.Fatalf("FailWithCommit(valid) error = %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != fruntime.TaskStatusFailed || result.Run == nil || result.Run.Status != fruntime.RunStatusFailed {
		t.Fatalf("FailWithCommit(valid) = %#v, %#v", tasks, result)
	}
	attempts, err := store.ListAttempts(context.Background(), claimed.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != fruntime.AttemptStatusFailed {
		t.Fatalf("ListAttempts() = %#v, %v", attempts, err)
	}
}

func TestCompleteWithCommitReplaysCommittedTaskTransition(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()
	now := time.Now().UTC()
	run := testRun("run-complete-replay", now)
	if _, err := store.Commit(context.Background(), fruntime.Commit{
		TransactionID: "create-complete-run",
		Run:           &fruntime.RunWrite{Mode: fruntime.RunWriteCreate, Run: run},
	}); err != nil {
		t.Fatalf("Commit(create) error = %v", err)
	}
	if _, err := store.Enqueue(context.Background(), fruntime.Task{
		ID: "task-complete-replay", Kind: fruntime.TaskKindGraphNode, RunID: run.RunID,
		Status: fruntime.TaskStatusQueued, CreatedAt: now, UpdatedAt: now, AvailableAt: now,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	claimed, _, err := store.Claim(context.Background(), fruntime.WorkerIdentity{ID: "worker-complete"}, fruntime.TaskClaimOptions{
		TaskID: "task-complete-replay", Now: now, TTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	persistedRun, err := store.GetRun(context.Background(), run.RunID)
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	persistedRun.Status = fruntime.RunStatusCompleted
	persistedRun.UpdatedAt = now.Add(time.Second)
	persistedRun.FinishedAt = &persistedRun.UpdatedAt
	commit := fruntime.Commit{
		TransactionID: "complete-replay",
		Run:           &fruntime.RunWrite{Mode: fruntime.RunWriteUpdate, Run: persistedRun},
	}
	result := fruntime.TaskResult{Payload: []byte(`{"status":"completed"}`)}
	completed, firstResult, err := store.CompleteWithCommit(context.Background(), *claimed.Lease, result, commit)
	if err != nil {
		t.Fatalf("CompleteWithCommit(first) error = %v", err)
	}
	replayed, replayResult, err := store.CompleteWithCommit(context.Background(), *claimed.Lease, result, commit)
	if err != nil {
		t.Fatalf("CompleteWithCommit(replay) error = %v", err)
	}
	if !tasksEqual(completed, replayed) || replayResult.TransactionID != firstResult.TransactionID || replayResult.Outcome != fruntime.TransactionCommitted {
		t.Fatalf("CompleteWithCommit(replay) = %#v, %#v; first = %#v, %#v", replayed, replayResult, completed, firstResult)
	}
	if _, _, err := store.CompleteWithCommit(context.Background(), *claimed.Lease, fruntime.TaskResult{Payload: []byte(`{"status":"different"}`)}, commit); err == nil {
		t.Fatal("CompleteWithCommit(mismatched replay) error = nil")
	}
}

func TestFailWithCommitReplaysCommittedTaskTransition(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()
	now := time.Now().UTC()
	run := testRun("run-fail-replay", now)
	if _, err := store.Commit(context.Background(), fruntime.Commit{
		TransactionID: "create-fail-run",
		Run:           &fruntime.RunWrite{Mode: fruntime.RunWriteCreate, Run: run},
	}); err != nil {
		t.Fatalf("Commit(create) error = %v", err)
	}
	if _, err := store.Enqueue(context.Background(), fruntime.Task{
		ID: "task-fail-replay", Kind: fruntime.TaskKindGraphNode, RunID: run.RunID,
		Status: fruntime.TaskStatusQueued, CreatedAt: now, UpdatedAt: now, AvailableAt: now,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	claimed, _, err := store.Claim(context.Background(), fruntime.WorkerIdentity{ID: "worker-fail"}, fruntime.TaskClaimOptions{
		TaskID: "task-fail-replay", Now: now, TTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	persistedRun, err := store.GetRun(context.Background(), run.RunID)
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	persistedRun.Status = fruntime.RunStatusFailed
	persistedRun.ErrorMessage = "failed"
	persistedRun.UpdatedAt = now.Add(time.Second)
	persistedRun.FinishedAt = &persistedRun.UpdatedAt
	failures := []fruntime.TaskFailureTransition{{Lease: *claimed.Lease, Failure: fruntime.TaskFailure{Message: "failed"}}}
	commit := fruntime.Commit{
		TransactionID: "fail-replay",
		Run:           &fruntime.RunWrite{Mode: fruntime.RunWriteUpdate, Run: persistedRun},
	}
	failed, firstResult, err := store.FailWithCommit(context.Background(), failures, commit)
	if err != nil {
		t.Fatalf("FailWithCommit(first) error = %v", err)
	}
	replayed, replayResult, err := store.FailWithCommit(context.Background(), failures, commit)
	if err != nil {
		t.Fatalf("FailWithCommit(replay) error = %v", err)
	}
	if len(failed) != 1 || len(replayed) != 1 || !tasksEqual(failed[0], replayed[0]) || replayResult.TransactionID != firstResult.TransactionID || replayResult.Outcome != fruntime.TransactionCommitted {
		t.Fatalf("FailWithCommit(replay) = %#v, %#v; first = %#v, %#v", replayed, replayResult, failed, firstResult)
	}
	mismatched := []fruntime.TaskFailureTransition{{Lease: *claimed.Lease, Failure: fruntime.TaskFailure{Message: "different"}}}
	if _, _, err := store.FailWithCommit(context.Background(), mismatched, commit); err == nil {
		t.Fatal("FailWithCommit(mismatched replay) error = nil")
	}
}

func TestEnqueueWithCommitRejectsTransactionReuseForDifferentTask(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()
	now := time.Now().UTC()
	run := testRun("run-enqueue-conflict", now)
	commit := fruntime.Commit{
		TransactionID: "enqueue-conflict",
		Run:           &fruntime.RunWrite{Mode: fruntime.RunWriteCreate, Run: run},
	}
	first := fruntime.Task{ID: "task-enqueue-first", Kind: fruntime.TaskKindGraphRun, RunID: run.RunID, Status: fruntime.TaskStatusQueued, CreatedAt: now, UpdatedAt: now, AvailableAt: now}
	if _, _, err := store.EnqueueWithCommit(context.Background(), first, commit); err != nil {
		t.Fatalf("EnqueueWithCommit(first) error = %v", err)
	}
	second := first
	second.ID = "task-enqueue-second"
	if _, _, err := store.EnqueueWithCommit(context.Background(), second, commit); err == nil {
		t.Fatal("EnqueueWithCommit(second) error = nil")
	}
	if _, err := store.GetTask(context.Background(), second.ID); !errors.Is(err, fruntime.ErrTaskNotFound) {
		t.Fatalf("GetTask(second) error = %v, want task not found", err)
	}
}

func TestReopenedStoreTakesOverExpiredLeaseAndFencesOldOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.db")
	firstStore, err := Open(path)
	if err != nil {
		t.Fatalf("Open(first) error = %v", err)
	}
	now := time.Now().UTC()
	if _, err := firstStore.Enqueue(context.Background(), fruntime.Task{ID: "task-reopen", Kind: "test", Status: fruntime.TaskStatusQueued, CreatedAt: now, UpdatedAt: now, AvailableAt: now}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	first, _, err := firstStore.Claim(context.Background(), fruntime.WorkerIdentity{ID: "worker-before-restart"}, fruntime.TaskClaimOptions{TaskID: "task-reopen", Now: now, TTL: time.Second})
	if err != nil {
		t.Fatalf("Claim(first) error = %v", err)
	}
	if err := firstStore.Close(); err != nil {
		t.Fatalf("Close(first) error = %v", err)
	}
	secondStore, err := Open(path)
	if err != nil {
		t.Fatalf("Open(second) error = %v", err)
	}
	defer func() { _ = secondStore.Close() }()
	second, _, err := secondStore.Claim(context.Background(), fruntime.WorkerIdentity{ID: "worker-after-restart"}, fruntime.TaskClaimOptions{TaskID: "task-reopen", Now: now.Add(2 * time.Second), TTL: time.Minute})
	if err != nil {
		t.Fatalf("Claim(second) error = %v", err)
	}
	if first.Lease.Epoch+1 != second.Lease.Epoch {
		t.Fatalf("lease epoch = %d, want %d", second.Lease.Epoch, first.Lease.Epoch+1)
	}
	if _, err := secondStore.Complete(context.Background(), *first.Lease, fruntime.TaskResult{}); !errors.Is(err, fruntime.ErrTaskLeaseLost) {
		t.Fatalf("Complete(old lease) error = %v, want task lease lost", err)
	}
	if _, err := secondStore.Complete(context.Background(), *second.Lease, fruntime.TaskResult{}); err != nil {
		t.Fatalf("Complete(new lease) error = %v", err)
	}
	attempts, err := secondStore.ListAttempts(context.Background(), "task-reopen")
	if err != nil || len(attempts) != 2 || attempts[0].Status != fruntime.AttemptStatusAbandoned || attempts[1].Status != fruntime.AttemptStatusCompleted {
		t.Fatalf("ListAttempts() = %#v, %v", attempts, err)
	}
}

func TestRunDeletionFenceRejectsTaskTransitions(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()
	now := time.Now().UTC()
	run := testRun("run-task-fence", now)
	if _, err := store.Commit(context.Background(), fruntime.Commit{
		TransactionID: "create-fenced-run",
		Run:           &fruntime.RunWrite{Mode: fruntime.RunWriteCreate, Run: run},
	}); err != nil {
		t.Fatalf("Commit(create) error = %v", err)
	}
	if _, err := store.Enqueue(context.Background(), fruntime.Task{ID: "task-fence", Kind: "test", RunID: run.RunID, Status: fruntime.TaskStatusQueued, CreatedAt: now, UpdatedAt: now, AvailableAt: now}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	claimed, _, err := store.Claim(context.Background(), fruntime.WorkerIdentity{ID: "worker-fence"}, fruntime.TaskClaimOptions{TaskID: "task-fence", Now: now, TTL: time.Minute})
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	deletionContext := fruntime.WithRunDeletionMutation(context.Background(), "delete-task-fence")
	if err := store.FenceRunDeletion(deletionContext, run.RunID, "delete-task-fence"); err != nil {
		t.Fatalf("FenceRunDeletion() error = %v", err)
	}
	if _, err := store.Heartbeat(context.Background(), *claimed.Lease, now.Add(time.Second), time.Minute); !errors.Is(err, fruntime.ErrRunControlNotAllowed) {
		t.Fatalf("Heartbeat() error = %v, want run control not allowed", err)
	}
	if _, err := store.Complete(context.Background(), *claimed.Lease, fruntime.TaskResult{}); !errors.Is(err, fruntime.ErrRunControlNotAllowed) {
		t.Fatalf("Complete() error = %v, want run control not allowed", err)
	}
	if _, err := store.Fail(context.Background(), *claimed.Lease, fruntime.TaskFailure{Message: "late failure"}); !errors.Is(err, fruntime.ErrRunControlNotAllowed) {
		t.Fatalf("Fail() error = %v, want run control not allowed", err)
	}
	if _, err := store.Cancel(context.Background(), claimed.ID, claimed.Version, now.Add(time.Second)); !errors.Is(err, fruntime.ErrRunControlNotAllowed) {
		t.Fatalf("Cancel() error = %v, want run control not allowed", err)
	}
	persisted, err := store.GetTask(context.Background(), claimed.ID)
	if err != nil || persisted.Status != fruntime.TaskStatusRunning || persisted.Lease == nil {
		t.Fatalf("GetTask() = %#v, %v", persisted, err)
	}
}

func TestGraphRunTaskCannotCompleteBeforeRunIsTerminal(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()
	now := time.Now().UTC()
	run := testRun("run-not-terminal", now)
	if _, err := store.Commit(context.Background(), fruntime.Commit{
		TransactionID: "create-non-terminal-run",
		Run:           &fruntime.RunWrite{Mode: fruntime.RunWriteCreate, Run: run},
	}); err != nil {
		t.Fatalf("Commit(create) error = %v", err)
	}
	if _, err := store.Enqueue(context.Background(), fruntime.Task{
		ID: run.RunID, Kind: fruntime.TaskKindGraphRun, RunID: run.RunID,
		Status: fruntime.TaskStatusQueued, CreatedAt: now, UpdatedAt: now, AvailableAt: now,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	claimed, _, err := store.Claim(context.Background(), fruntime.WorkerIdentity{ID: "worker-non-terminal"}, fruntime.TaskClaimOptions{TaskID: run.RunID, Now: now, TTL: time.Minute})
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if _, err := store.Complete(context.Background(), *claimed.Lease, fruntime.TaskResult{}); !errors.Is(err, fruntime.ErrTaskConflict) {
		t.Fatalf("Complete() error = %v, want task conflict", err)
	}
	persisted, err := store.GetTask(context.Background(), claimed.ID)
	if err != nil || persisted.Status != fruntime.TaskStatusRunning || persisted.Lease == nil {
		t.Fatalf("GetTask() = %#v, %v", persisted, err)
	}
}

func testRun(runID string, now time.Time) fruntime.RunRecord {
	return fruntime.RunRecord{
		RunID: runID, RootRunID: runID, RunPath: []string{runID}, Namespace: runID,
		GraphID: "graph", GraphVersion: "v1", Status: fruntime.RunStatusPending,
		EntryNodeID: "start", CurrentNodeID: "start", StartedAt: now, UpdatedAt: now,
	}
}
