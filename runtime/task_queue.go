package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrTaskNotFound  = errors.New("task not found")
	ErrTaskConflict  = errors.New("task conflict")
	ErrTaskLeaseHeld = errors.New("task lease is held")
	ErrTaskLeaseLost = errors.New("task lease is lost")
)

const TaskKindGraphRun = "graph.run"
const TaskKindGraphNode = "graph.node"

type TaskStatus string

const (
	TaskStatusQueued    TaskStatus = "queued"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusCanceled  TaskStatus = "canceled"
	TaskStatusDead      TaskStatus = "dead"
)

type AttemptStatus string

const (
	AttemptStatusRunning   AttemptStatus = "running"
	AttemptStatusCompleted AttemptStatus = "completed"
	AttemptStatusFailed    AttemptStatus = "failed"
	AttemptStatusCanceled  AttemptStatus = "canceled"
	AttemptStatusAbandoned AttemptStatus = "abandoned"
)

type WorkerIdentity struct {
	ID       string            `json:"id"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type TaskLease struct {
	TaskID      string    `json:"task_id"`
	WorkerID    string    `json:"worker_id"`
	Token       string    `json:"token"`
	Epoch       uint64    `json:"epoch"`
	AcquiredAt  time.Time `json:"acquired_at"`
	HeartbeatAt time.Time `json:"heartbeat_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type Task struct {
	ID             string          `json:"id"`
	Kind           string          `json:"kind"`
	RunID          string          `json:"run_id,omitempty"`
	StepID         string          `json:"step_id,omitempty"`
	GraphTaskID    string          `json:"graph_task_id,omitempty"`
	OperationID    string          `json:"operation_id,omitempty"`
	GraphID        string          `json:"graph_id,omitempty"`
	GraphSessionID string          `json:"graph_session_id,omitempty"`
	CheckpointID   string          `json:"checkpoint_id,omitempty"`
	Payload        json.RawMessage `json:"payload,omitempty"`
	Status         TaskStatus      `json:"status"`
	Version        uint64          `json:"version"`
	AttemptCount   int             `json:"attempt_count"`
	MaxAttempts    int             `json:"max_attempts"`
	AvailableAt    time.Time       `json:"available_at"`
	Lease          *TaskLease      `json:"lease,omitempty"`
	LastError      string          `json:"last_error,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	CompletedAt    *time.Time      `json:"completed_at,omitempty"`
}

type Attempt struct {
	ID          string        `json:"id"`
	TaskID      string        `json:"task_id"`
	Number      int           `json:"number"`
	WorkerID    string        `json:"worker_id"`
	LeaseToken  string        `json:"lease_token"`
	LeaseEpoch  uint64        `json:"lease_epoch"`
	Status      AttemptStatus `json:"status"`
	StartedAt   time.Time     `json:"started_at"`
	HeartbeatAt time.Time     `json:"heartbeat_at"`
	FinishedAt  *time.Time    `json:"finished_at,omitempty"`
	Error       string        `json:"error,omitempty"`
}

type TaskClaimOptions struct {
	TaskID string
	Kinds  []string
	Now    time.Time
	TTL    time.Duration
}

type TaskResult struct {
	Payload json.RawMessage `json:"payload,omitempty"`
}

type TaskFailure struct {
	Message   string
	RetryAt   time.Time
	Retryable bool
}

type TaskFailureTransition struct {
	Lease   TaskLease
	Failure TaskFailure
}

type taskCompletionKey struct{}
type taskFailuresKey struct{}

func withTaskCompletion(ctx context.Context, lease TaskLease) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, taskCompletionKey{}, lease)
}

func taskCompletionFromContext(ctx context.Context) (TaskLease, bool) {
	if ctx == nil {
		return TaskLease{}, false
	}
	lease, ok := ctx.Value(taskCompletionKey{}).(TaskLease)
	return lease, ok && lease.TaskID != ""
}

func withTaskFailures(ctx context.Context, failures []TaskFailureTransition) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, taskFailuresKey{}, append([]TaskFailureTransition(nil), failures...))
}

func taskFailuresFromContext(ctx context.Context) []TaskFailureTransition {
	if ctx == nil {
		return nil
	}
	failures, _ := ctx.Value(taskFailuresKey{}).([]TaskFailureTransition)
	return append([]TaskFailureTransition(nil), failures...)
}

type TaskFilter struct {
	Statuses []TaskStatus
	Kinds    []string
	RunID    string
	Limit    int
}

type TaskQueue interface {
	Enqueue(context.Context, Task) (Task, error)
	Claim(context.Context, WorkerIdentity, TaskClaimOptions) (Task, Attempt, error)
	Heartbeat(context.Context, TaskLease, time.Time, time.Duration) (TaskLease, error)
	Complete(context.Context, TaskLease, TaskResult) (Task, error)
	Fail(context.Context, TaskLease, TaskFailure) (Task, error)
	Cancel(context.Context, string, uint64, time.Time) (Task, error)
	GetTask(context.Context, string) (Task, error)
	ListTasks(context.Context, TaskFilter) ([]Task, error)
	ListAttempts(context.Context, string) ([]Attempt, error)
}

type AtomicTaskQueue interface {
	TaskQueue
	EnqueueWithCommit(context.Context, Task, Commit) (Task, CommitResult, error)
	CompleteWithCommit(context.Context, TaskLease, TaskResult, Commit) (Task, CommitResult, error)
	FailWithCommit(context.Context, []TaskFailureTransition, Commit) ([]Task, CommitResult, error)
}
