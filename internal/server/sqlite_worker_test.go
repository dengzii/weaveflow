package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	wfgraph "github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
	"github.com/gin-gonic/gin"
)

func TestSQLiteWorkerExecutesQueuedRun(t *testing.T) {
	workflow := wfgraph.NewGraph(nil)
	work := node.NewFuncNode(node.Spec{ID: "work", Name: "work"}, func(core.Context, *state.Access) (core.NodeResult, error) {
		return core.Success(), nil
	})
	if err := workflow.AddNode(work); err != nil {
		t.Fatalf("AddNode() error = %v", err)
	}
	if err := workflow.SetNodeSpec(dsl.GraphNodeSpec{ID: "work", Type: "test", Name: "work"}); err != nil {
		t.Fatalf("SetNodeSpec() error = %v", err)
	}
	if err := workflow.SetEntryPoint(work.ID()); err != nil {
		t.Fatalf("SetEntryPoint() error = %v", err)
	}
	if err := workflow.SetFinishPoint(work.ID()); err != nil {
		t.Fatalf("SetFinishPoint() error = %v", err)
	}
	srv, err := New(context.Background(), Config{
		Graph: workflow, GraphID: "graph", GraphVersion: "v1", GraphSessionID: "session-1",
		BaseDir: t.TempDir(), RuntimeStoreBackend: RuntimeStoreSQLite,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	queue := srv.runtime.taskQueue("graph")
	if queue == nil {
		t.Fatal("SQLite task queue is nil")
	}
	run, task, err := srv.Runner().EnqueueStart(context.Background(), state.NewState(), queue)
	if err != nil {
		t.Fatalf("EnqueueStart() error = %v", err)
	}
	if run.Status != runtime.RunStatusPending || task.Status != runtime.TaskStatusQueued {
		t.Fatalf("queued run/task = %#v / %#v", run, task)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		persistedTask, taskErr := queue.GetTask(context.Background(), task.ID)
		persistedRun, runErr := srv.Runner().GetRun(context.Background(), run.RunID)
		if taskErr == nil && runErr == nil && persistedTask.Status == runtime.TaskStatusCompleted && persistedRun.Status == runtime.RunStatusCompleted {
			if persistedTask.AttemptCount != 1 {
				t.Fatalf("attempt count = %d, want 1", persistedTask.AttemptCount)
			}
			nodeTasks, err := queue.ListTasks(context.Background(), runtime.TaskFilter{Kinds: []string{runtime.TaskKindGraphNode}, RunID: run.RunID})
			if err != nil {
				t.Fatalf("ListTasks(node) error = %v", err)
			}
			if len(nodeTasks) != 1 || nodeTasks[0].Status != runtime.TaskStatusCompleted || nodeTasks[0].AttemptCount != 1 || nodeTasks[0].OperationID == "" {
				t.Fatalf("node tasks = %#v", nodeTasks)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	persistedTask, _ := queue.GetTask(context.Background(), task.ID)
	persistedRun, _ := srv.Runner().GetRun(context.Background(), run.RunID)
	t.Fatalf("queued run did not complete: task=%#v run=%#v", persistedTask, persistedRun)
}

func TestSQLiteWorkerFailsNodeTaskWithRun(t *testing.T) {
	workflow := wfgraph.NewGraph(nil)
	work := node.NewFuncNode(node.Spec{ID: "work", Name: "work"}, func(core.Context, *state.Access) (core.NodeResult, error) {
		return core.NodeResult{}, errors.New("work failed")
	})
	if err := workflow.AddNode(work); err != nil {
		t.Fatalf("AddNode() error = %v", err)
	}
	if err := workflow.SetNodeSpec(dsl.GraphNodeSpec{ID: "work", Type: "test", Name: "work"}); err != nil {
		t.Fatalf("SetNodeSpec() error = %v", err)
	}
	if err := workflow.SetEntryPoint(work.ID()); err != nil {
		t.Fatalf("SetEntryPoint() error = %v", err)
	}
	if err := workflow.SetFinishPoint(work.ID()); err != nil {
		t.Fatalf("SetFinishPoint() error = %v", err)
	}
	srv, err := New(context.Background(), Config{
		Graph: workflow, GraphID: "graph", GraphVersion: "v1", GraphSessionID: "session-1",
		BaseDir: t.TempDir(), RuntimeStoreBackend: RuntimeStoreSQLite,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	queue := srv.runtime.taskQueue("graph")
	if queue == nil {
		t.Fatal("SQLite task queue is nil")
	}
	run, _, err := srv.Runner().EnqueueStart(context.Background(), state.NewState(), queue)
	if err != nil {
		t.Fatalf("EnqueueStart() error = %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		persistedRun, runErr := srv.Runner().GetRun(context.Background(), run.RunID)
		nodeTasks, taskErr := queue.ListTasks(context.Background(), runtime.TaskFilter{Kinds: []string{runtime.TaskKindGraphNode}, RunID: run.RunID})
		if runErr == nil && taskErr == nil && persistedRun.Status == runtime.RunStatusFailed && len(nodeTasks) == 1 && nodeTasks[0].Status == runtime.TaskStatusFailed {
			attempts, err := queue.ListAttempts(context.Background(), nodeTasks[0].ID)
			if err != nil || len(attempts) != 1 || attempts[0].Status != runtime.AttemptStatusFailed {
				t.Fatalf("node attempts = %#v, %v", attempts, err)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	persistedRun, _ := srv.Runner().GetRun(context.Background(), run.RunID)
	nodeTasks, _ := queue.ListTasks(context.Background(), runtime.TaskFilter{Kinds: []string{runtime.TaskKindGraphNode}, RunID: run.RunID})
	t.Fatalf("queued run did not fail: run=%#v node_tasks=%#v", persistedRun, nodeTasks)
}

func TestSQLiteUserInputInterruptResumeReusesQueuedNodeTask(t *testing.T) {
	srv, err := New(context.Background(), Config{
		Graph: newMinimalTestGraph(t), GraphID: "graph", GraphVersion: "v1", GraphSessionID: "session-1",
		BaseDir: t.TempDir(), RuntimeStoreBackend: RuntimeStoreSQLite,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	queue := srv.runtime.taskQueue("graph")
	if queue == nil {
		t.Fatal("SQLite task queue is nil")
	}
	runner := srv.Runner()
	pausedRun, _, err := runner.Start(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if pausedRun.Status != runtime.RunStatusPaused {
		t.Fatalf("paused run status = %q, want %q", pausedRun.Status, runtime.RunStatusPaused)
	}
	nodeTasks, err := queue.ListTasks(context.Background(), runtime.TaskFilter{Kinds: []string{runtime.TaskKindGraphNode}, RunID: pausedRun.RunID})
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(nodeTasks) != 1 || nodeTasks[0].Status != runtime.TaskStatusQueued || nodeTasks[0].Lease != nil {
		t.Fatalf("paused node tasks = %#v, want one queued task without lease", nodeTasks)
	}
	resumeInput := state.NewState()
	if err := state.SetPath(resumeInput, state.Shared("request", "pending_input").String(), "hello"); err != nil {
		t.Fatalf("SetPath() error = %v", err)
	}
	completedRun, finalState, err := runner.Resume(context.Background(), pausedRun.RunID, resumeInput)
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if completedRun.Status != runtime.RunStatusCompleted {
		t.Fatalf("resumed run status = %q, want %q", completedRun.Status, runtime.RunStatusCompleted)
	}
	value, ok := state.ReadPath(finalState, state.Shared("request", "input").String())
	if !ok || value != "hello" {
		t.Fatalf("final input = %#v, found = %v, want hello", value, ok)
	}
	nodeTasks, err = queue.ListTasks(context.Background(), runtime.TaskFilter{Kinds: []string{runtime.TaskKindGraphNode}, RunID: pausedRun.RunID})
	if err != nil {
		t.Fatalf("ListTasks(completed) error = %v", err)
	}
	if len(nodeTasks) != 1 || nodeTasks[0].Status != runtime.TaskStatusCompleted || nodeTasks[0].AttemptCount != 2 {
		t.Fatalf("completed node tasks = %#v, want one task completed on second attempt", nodeTasks)
	}
}

func TestSQLiteWorkerRestartsAndTakesOverExpiredRun(t *testing.T) {
	gin.SetMode(gin.TestMode)
	baseDir := t.TempDir()
	first, err := New(context.Background(), Config{BaseDir: baseDir, RuntimeStoreBackend: RuntimeStoreSQLite})
	if err != nil {
		t.Fatalf("New(first) error = %v", err)
	}
	firstClosed := false
	t.Cleanup(func() {
		if !firstClosed {
			_ = first.Close()
		}
	})
	engine := gin.New()
	first.RegisterRoutes(engine.Group(""))
	graph := putGraphForHashTest(t, engine, triggerGraphUploadBody("restart-graph", "v1", "restart"))
	if err := first.runtime.ensureWorker(graph.Graph.ID); err != nil {
		t.Fatalf("ensureWorker() error = %v", err)
	}
	queue := first.runtime.taskQueue(graph.Graph.ID)
	if queue == nil {
		t.Fatal("SQLite task queue is nil")
	}
	first.runtime.mu.Lock()
	cancel := first.runtime.workers[graph.Graph.ID]
	done := first.runtime.workerDone[graph.Graph.ID]
	delete(first.runtime.workers, graph.Graph.ID)
	delete(first.runtime.workerDone, graph.Graph.ID)
	first.runtime.mu.Unlock()
	if cancel == nil || done == nil {
		t.Fatal("durable worker was not started")
	}
	cancel()
	<-done
	run, task, err := graphRunnerForSession(t, first, graph).EnqueueStart(context.Background(), state.NewState(), queue)
	if err != nil {
		t.Fatalf("EnqueueStart() error = %v", err)
	}
	if task.Status != runtime.TaskStatusQueued || run.Status != runtime.RunStatusPending {
		t.Fatalf("queued run/task = %#v / %#v", run, task)
	}
	claimed, _, err := queue.Claim(context.Background(), runtime.WorkerIdentity{ID: "worker-before-restart"}, runtime.TaskClaimOptions{
		TaskID: task.ID, Now: time.Now().UTC(), TTL: time.Nanosecond,
	})
	if err != nil {
		t.Fatalf("Claim(before restart) error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close(first) error = %v", err)
	}
	firstClosed = true

	restarted, err := New(context.Background(), Config{BaseDir: baseDir, RuntimeStoreBackend: RuntimeStoreSQLite})
	if err != nil {
		t.Fatalf("New(restarted) error = %v", err)
	}
	t.Cleanup(func() {
		if err := restarted.Close(); err != nil {
			t.Errorf("Close(restarted) error = %v", err)
		}
	})
	restartedQueue := restarted.runtime.taskQueue(graph.Graph.ID)
	if restartedQueue == nil {
		t.Fatal("restarted SQLite task queue is nil")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		persistedTask, taskErr := restartedQueue.GetTask(context.Background(), task.ID)
		persistedRun, runErr := restarted.runtime.session(graph.Graph.ID, graph.Graph.GraphSessionID).runner.GetRun(context.Background(), run.RunID)
		if taskErr == nil && runErr == nil && persistedTask.Status == runtime.TaskStatusCompleted && persistedRun.Status == runtime.RunStatusCompleted {
			if persistedTask.AttemptCount != 2 {
				t.Fatalf("attempt count = %d, want 2", persistedTask.AttemptCount)
			}
			attempts, err := restartedQueue.ListAttempts(context.Background(), task.ID)
			if err != nil || len(attempts) != 2 || attempts[0].Status != runtime.AttemptStatusAbandoned || attempts[1].Status != runtime.AttemptStatusCompleted {
				t.Fatalf("ListAttempts() = %#v, %v", attempts, err)
			}
			if _, err := restartedQueue.Complete(context.Background(), *claimed.Lease, runtime.TaskResult{}); !errors.Is(err, runtime.ErrTaskLeaseLost) {
				t.Fatalf("Complete(old lease) error = %v, want task lease lost", err)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	persistedTask, _ := restartedQueue.GetTask(context.Background(), task.ID)
	persistedRun, _ := restarted.runtime.session(graph.Graph.ID, graph.Graph.GraphSessionID).runner.GetRun(context.Background(), run.RunID)
	t.Fatalf("restarted queued run did not complete: task=%#v run=%#v", persistedTask, persistedRun)
}

func TestSQLiteWorkerFailsClosedForUnknownEffect(t *testing.T) {
	workflow := wfgraph.NewGraph(nil)
	work := node.NewFuncNode(node.Spec{ID: "work", Name: "work"}, func(core.Context, *state.Access) (core.NodeResult, error) {
		return core.Success(), nil
	})
	if err := workflow.AddNode(work); err != nil {
		t.Fatalf("AddNode() error = %v", err)
	}
	if err := workflow.SetNodeSpec(dsl.GraphNodeSpec{ID: "work", Type: "test", Name: "work"}); err != nil {
		t.Fatalf("SetNodeSpec() error = %v", err)
	}
	if err := workflow.SetEntryPoint(work.ID()); err != nil {
		t.Fatalf("SetEntryPoint() error = %v", err)
	}
	if err := workflow.SetFinishPoint(work.ID()); err != nil {
		t.Fatalf("SetFinishPoint() error = %v", err)
	}
	server, err := New(context.Background(), Config{
		Graph: workflow, GraphID: "effect-graph", GraphVersion: "v1", GraphSessionID: "effect-session",
		BaseDir: t.TempDir(), RuntimeStoreBackend: RuntimeStoreSQLite,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	runner := server.Runner()
	queue := server.runtime.taskQueue("effect-graph")
	if queue == nil {
		t.Fatal("SQLite task queue is nil")
	}
	now := time.Now().UTC()
	finishedAt := now
	run := runtime.RunRecord{
		RunID: "run-effect-unknown", RootRunID: "run-effect-unknown", RunPath: []string{"run-effect-unknown"}, Namespace: "run-effect-unknown",
		GraphID: runner.GraphID(), GraphVersion: runner.GraphVersion(), GraphHash: runner.GraphHash(), GraphSnapshotHash: runner.GraphSnapshotHash(), GraphSessionID: runner.GraphSessionID(),
		Status: runtime.RunStatusFailed, EntryNodeID: "work", CurrentNodeID: "work", StartedAt: now, UpdatedAt: now, FinishedAt: &finishedAt,
	}
	if err := runner.ExecutionStore().CreateRun(context.Background(), run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	step := runtime.StepRecord{
		StepID: "step-effect-unknown", RunID: run.RunID, TaskID: "node-task-effect-unknown", NodeID: "work",
		OperationKey: "operation-effect-unknown", EffectClass: core.EffectNonIdempotentWrite, EffectStatus: core.EffectUnknown,
		Status: runtime.StepStatusFailed, Attempt: 1, StartedAt: now, UpdatedAt: now, FinishedAt: &finishedAt,
	}
	if err := runner.ExecutionStore().AppendStep(context.Background(), step); err != nil {
		t.Fatalf("AppendStep() error = %v", err)
	}
	task, err := queue.Enqueue(context.Background(), runtime.Task{
		ID: run.RunID, Kind: runtime.TaskKindGraphRun, RunID: run.RunID, GraphTaskID: run.EntryNodeID,
		GraphID: run.GraphID, GraphSessionID: run.GraphSessionID, Status: runtime.TaskStatusQueued,
		MaxAttempts: 3, AvailableAt: now, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if err := server.runtime.ensureWorker(run.GraphID); err != nil {
		t.Fatalf("ensureWorker() error = %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		persistedTask, taskErr := queue.GetTask(context.Background(), task.ID)
		if taskErr == nil && persistedTask.Status == runtime.TaskStatusFailed {
			if persistedTask.AttemptCount != 1 || !strings.Contains(persistedTask.LastError, "unresolved effect") {
				t.Fatalf("failed task = %#v", persistedTask)
			}
			persistedRun, runErr := runner.GetRun(context.Background(), run.RunID)
			if runErr != nil || persistedRun.Status != runtime.RunStatusFailed {
				t.Fatalf("GetRun() = %#v, %v", persistedRun, runErr)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	persistedTask, _ := queue.GetTask(context.Background(), task.ID)
	t.Fatalf("unknown effect task did not fail closed: %#v", persistedTask)
}

func graphRunnerForSession(t *testing.T, server *Server, graph graphLoadResponse) *runtime.GraphRunner {
	t.Helper()
	session := server.runtime.session(graph.Graph.ID, graph.Graph.GraphSessionID)
	if session.runner == nil {
		t.Fatalf("graph session %q is not loaded", graph.Graph.GraphSessionID)
	}
	return session.runner
}
