package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/state"
)

func TestExecutionLeaseAllowsOneOwnerAndFencesExpiredOwner(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewMemoryRuntimeStore()
	artifactStore := NewMemoryArtifactStore()
	now := time.Unix(1_000, 0).UTC()
	if err := store.CreateRun(ctx, RunRecord{
		RunID: "run", RootRunID: "run", RunPath: []string{"run"}, Namespace: "run",
		GraphID: "graph", Status: RunStatusRunning, StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	currentTime := now
	newRunner := func(ownerID string) *GraphRunner {
		return &GraphRunner{
			executionStore:   store,
			artifactStore:    artifactStore,
			transactionStore: store,
			leaseOwnerID:     ownerID,
			leaseTTL:         time.Minute,
			now:              func() time.Time { return currentTime },
		}
	}
	firstRunner := newRunner("owner-one")
	secondRunner := newRunner("owner-two")
	thirdRunner := newRunner("owner-three")

	firstRun, firstGuard, err := firstRunner.acquireExecutionLease(ctx, "run")
	if err != nil {
		t.Fatalf("first acquireExecutionLease() error = %v", err)
	}
	if firstRun.ExecutionLease == nil || firstRun.ExecutionLease.Epoch != 1 {
		t.Fatalf("first execution lease = %#v", firstRun.ExecutionLease)
	}
	if _, err := firstRunner.commitRuntime(ctx, Commit{
		Run: &RunWrite{Mode: RunWriteCheck, Run: firstRun},
	}); !errors.Is(err, ErrExecutionLeaseLost) {
		t.Fatalf("commitRuntime() without owner context error = %v, want lease lost", err)
	}
	if _, _, err := secondRunner.acquireExecutionLease(ctx, "run"); !errors.Is(err, ErrExecutionLeaseHeld) {
		t.Fatalf("second acquire before expiry error = %v, want held", err)
	}

	currentTime = now.Add(2 * time.Minute)
	type claimResult struct {
		runner *GraphRunner
		run    RunRecord
		guard  ExecutionLeaseGuard
		err    error
	}
	start := make(chan struct{})
	results := make(chan claimResult, 2)
	var wait sync.WaitGroup
	for _, runner := range []*GraphRunner{secondRunner, thirdRunner} {
		wait.Add(1)
		go func(candidate *GraphRunner) {
			defer wait.Done()
			<-start
			run, guard, claimErr := candidate.acquireExecutionLease(ctx, "run")
			results <- claimResult{runner: candidate, run: run, guard: guard, err: claimErr}
		}(runner)
	}
	close(start)
	wait.Wait()
	close(results)
	var winner claimResult
	successes := 0
	for result := range results {
		if result.err == nil {
			successes++
			winner = result
			continue
		}
		if !errors.Is(result.err, ErrExecutionLeaseHeld) {
			t.Fatalf("competing takeover error = %v", result.err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful takeovers = %d, want 1", successes)
	}
	if winner.run.ExecutionLease == nil || winner.run.ExecutionLease.Epoch != 2 || winner.guard.Token == firstGuard.Token {
		t.Fatalf("takeover lease = %#v, guard = %#v", winner.run.ExecutionLease, winner.guard)
	}

	latest, err := store.GetRun(ctx, "run")
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if _, err := store.Commit(ctx, Commit{
		Lease: &firstGuard,
		Run:   &RunWrite{Mode: RunWriteCheck, Run: latest},
	}); !errors.Is(err, ErrExecutionLeaseLost) {
		t.Fatalf("stale owner Commit() error = %v, want lease lost", err)
	}
	if _, err := store.Commit(ctx, Commit{
		Lease: &winner.guard,
		Run:   &RunWrite{Mode: RunWriteCheck, Run: latest},
	}); err != nil {
		t.Fatalf("current owner Commit() error = %v", err)
	}
	if _, err := firstRunner.recordArtifact(withExecutionLeaseGuard(ctx, firstGuard), "stale-artifact-transaction", Artifact{
		RunID: "run", Type: "text", Data: []byte("stale owner artifact"),
	}); !errors.Is(err, ErrExecutionLeaseLost) {
		t.Fatalf("stale owner recordArtifact() error = %v, want lease lost", err)
	}
	if artifacts, err := artifactStore.List(ctx, "run"); err != nil || len(artifacts) != 0 {
		t.Fatalf("artifacts after stale owner write = %#v, error = %v", artifacts, err)
	}
	latest.ErrorMessage = "stale owner write"
	if _, err := store.CompareAndSwapRun(withExecutionLeaseGuard(ctx, firstGuard), latest.Revision, latest); !errors.Is(err, ErrExecutionLeaseLost) {
		t.Fatalf("stale owner CompareAndSwapRun() error = %v, want lease lost", err)
	}
	if err := winner.runner.releaseExecutionLease(ctx, winner.guard); err != nil {
		t.Fatalf("releaseExecutionLease() error = %v", err)
	}
	released, err := store.GetRun(ctx, "run")
	if err != nil {
		t.Fatalf("GetRun() after release error = %v", err)
	}
	if released.ExecutionLease == nil || released.ExecutionLease.Status != ExecutionLeaseReleased || released.ExecutionLease.Epoch != 2 {
		t.Fatalf("released execution lease = %#v", released.ExecutionLease)
	}
	if _, err := store.Commit(ctx, Commit{
		Lease: &firstGuard,
		Run:   &RunWrite{Mode: RunWriteCheck, Run: released},
	}); !errors.Is(err, ErrExecutionLeaseLost) {
		t.Fatalf("stale owner Commit() after release error = %v, want lease lost", err)
	}
	if _, err := winner.runner.recordArtifact(withExecutionLeaseGuard(ctx, winner.guard), "released-artifact-transaction", Artifact{
		RunID: "run", Type: "text", Data: []byte("released owner artifact"),
	}); !errors.Is(err, ErrExecutionLeaseLost) {
		t.Fatalf("released owner recordArtifact() error = %v, want lease lost", err)
	}
}

func TestExecutionLeaseHeartbeatExtendsExpiryAndDeletionWins(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewMemoryRuntimeStore()
	now := time.Unix(2_000, 0).UTC()
	run := RunRecord{
		RunID: "run", RootRunID: "run", RunPath: []string{"run"}, Namespace: "run",
		GraphID: "graph", Status: RunStatusPaused, StartedAt: now, UpdatedAt: now,
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	currentTime := now
	runner := &GraphRunner{
		executionStore: store,
		leaseOwnerID:   "owner",
		leaseTTL:       time.Minute,
		now:            func() time.Time { return currentTime },
	}
	_, guard, err := runner.acquireExecutionLease(ctx, run.RunID)
	if err != nil {
		t.Fatalf("acquireExecutionLease() error = %v", err)
	}
	currentTime = now.Add(30 * time.Second)
	if err := runner.heartbeatExecutionLease(ctx, guard); err != nil {
		t.Fatalf("heartbeatExecutionLease() error = %v", err)
	}
	heartbeatRun, err := store.GetRun(ctx, run.RunID)
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if !heartbeatRun.ExecutionLease.HeartbeatAt.Equal(currentTime) || !heartbeatRun.ExecutionLease.ExpiresAt.Equal(currentTime.Add(time.Minute)) {
		t.Fatalf("heartbeat execution lease = %#v", heartbeatRun.ExecutionLease)
	}
	heartbeatRun.Deletion = &RunDeletionState{ID: "deletion", RootRunID: run.RunID, Phase: RunDeletionReserved}
	if _, err := store.CompareAndSwapRun(withRunDeletionMutation(ctx, "deletion"), heartbeatRun.Revision, heartbeatRun); err != nil {
		t.Fatalf("reserve deletion error = %v", err)
	}
	if err := runner.heartbeatExecutionLease(ctx, guard); !errors.Is(err, ErrExecutionLeaseLost) {
		t.Fatalf("heartbeat after deletion error = %v, want lease lost", err)
	}
	if err := runner.releaseExecutionLease(ctx, guard); !errors.Is(err, ErrExecutionLeaseLost) {
		t.Fatalf("release after deletion error = %v, want lease lost", err)
	}
}

func TestInitialCheckpointAllowsTakeoverBeforeFirstNode(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewMemoryRuntimeStore()
	now := time.Unix(3_000, 0).UTC()
	workflow := &blockingChildRunGraph{entered: make(chan struct{}, 1), release: make(chan struct{})}
	close(workflow.release)
	newRunner := func(ownerID string) *GraphRunner {
		runner, err := NewGraphRunner(
			workflow,
			store,
			store,
			state.NewJSONStateCodec(""),
			store,
			WithRuntimeTransactionStore(store),
			WithExecutionLeasePolicy(ownerID, time.Minute, 20*time.Second),
			WithNow(func() time.Time { return now }),
		)
		if err != nil {
			t.Fatalf("NewGraphRunner(%q) error = %v", ownerID, err)
		}
		return runner
	}
	firstRunner := newRunner("owner-one")
	initial := state.NewState()
	if err := initial.SetSection("shared", map[string]any{"message": "persisted before execution"}); err != nil {
		t.Fatalf("SetSection() error = %v", err)
	}
	run, _, err := firstRunner.startRun(ctx, initial, nil)
	if err != nil {
		t.Fatalf("startRun() error = %v", err)
	}
	if run.LastCheckpointID == "" || run.ExecutionLease == nil || run.ExecutionLease.Epoch != 1 {
		t.Fatalf("initial run = %#v", run)
	}
	restored, err := firstRunner.LoadCheckpointState(ctx, run.LastCheckpointID)
	if err != nil {
		t.Fatalf("LoadCheckpointState() error = %v", err)
	}
	if value := restored.Business.Export()["shared"].(map[string]any)["message"]; value != "persisted before execution" {
		t.Fatalf("initial checkpoint message = %#v", value)
	}

	now = now.Add(2 * time.Minute)
	secondRunner := newRunner("owner-two")
	completed, finalState, err := secondRunner.Resume(ctx, run.RunID, nil)
	if err != nil {
		t.Fatalf("Resume() after initial checkpoint error = %v", err)
	}
	if completed.Status != RunStatusCompleted || completed.ExecutionLease == nil || completed.ExecutionLease.Status != ExecutionLeaseReleased || completed.ExecutionLease.Epoch != 2 {
		t.Fatalf("completed takeover run = %#v", completed)
	}
	if value := finalState.Export()["shared"].(map[string]any)["message"]; value != "persisted before execution" {
		t.Fatalf("resumed state message = %#v", value)
	}
}
