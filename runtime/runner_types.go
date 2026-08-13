// Package runtime manages runs, checkpoints, events, artifacts, and control transitions.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dengzii/weaveflow/state"
)

var (
	// ErrRunnerRecordNotFound reports that a persisted runtime record does not exist.
	ErrRunnerRecordNotFound = errors.New("runner record not found")
	// ErrRunControlNotAllowed reports that a run cannot accept the requested transition.
	ErrRunControlNotAllowed = errors.New("run control not allowed")
	// ErrRunRevisionConflict reports a failed compare-and-swap transition.
	ErrRunRevisionConflict = errors.New("run revision conflict")
)

type RunRevisionConflictError struct {
	RunID    string
	Expected uint64
	Actual   uint64
}

func (conflict *RunRevisionConflictError) Error() string {
	if conflict == nil {
		return ErrRunRevisionConflict.Error()
	}
	return fmt.Sprintf("%s for run %q: expected %d, actual %d", ErrRunRevisionConflict, conflict.RunID, conflict.Expected, conflict.Actual)
}

func (*RunRevisionConflictError) Unwrap() error { return ErrRunRevisionConflict }

type RunStatus string

const (
	RunStatusPending   RunStatus = "pending"
	RunStatusRunning   RunStatus = "running"
	RunStatusPaused    RunStatus = "paused"
	RunStatusFailed    RunStatus = "failed"
	RunStatusCompleted RunStatus = "completed"
	RunStatusCanceled  RunStatus = "canceled"
)

type StepStatus string

const (
	StepStatusScheduled StepStatus = "scheduled"
	StepStatusRunning   StepStatus = "running"
	StepStatusSucceeded StepStatus = "succeeded"
	StepStatusFailed    StepStatus = "failed"
	StepStatusPaused    StepStatus = "paused"
	StepStatusCanceled  StepStatus = "canceled"
)

type CheckpointStage string

const (
	CheckpointBeforeNode CheckpointStage = "before_node"
	CheckpointAfterNode  CheckpointStage = "after_node"
	CheckpointAfterWave  CheckpointStage = "after_wave"
	CheckpointFinal      CheckpointStage = "final"
)

type EventType string

const (
	EventRunCreated         EventType = "run.created"
	EventRunStarted         EventType = "run.started"
	EventRunPauseRequested  EventType = "run.pause_requested"
	EventRunPaused          EventType = "run.paused"
	EventRunResumed         EventType = "run.resumed"
	EventRunCancelRequested EventType = "run.cancel_requested"
	EventRunCanceled        EventType = "run.canceled"
	EventRunFinished        EventType = "run.finished"
	EventRunFailed          EventType = "run.failed"
	EventRunLimitExceeded   EventType = "run.limit_exceeded"
	EventRunBackpressure    EventType = "run.backpressure"
	EventNodeStarted        EventType = "nodes.started"
	EventNodeFinished       EventType = "nodes.finished"
	EventNodeFailed         EventType = "nodes.failed"
	EventNodeCanceled       EventType = "nodes.canceled"
	EventNodeRetry          EventType = "nodes.retry"
	EventConditionFailed    EventType = "condition.failed"
	EventNodeCustom         EventType = "nodes.custom"
	EventLLMReasoningChunk  EventType = "llm.reasoning_chunk"
	EventLLMContentChunk    EventType = "llm.content_chunk"
	EventLLMReasoning       EventType = "llm.reasoning"
	EventLLMContent         EventType = "llm.content"
	EventLLMFunctionCall    EventType = "llm.function_call"
	EventLLMUsage           EventType = "llm.usage"
	EventLLMCall            EventType = "llm.call"
	EventToolStarted        EventType = "tool.started"
	EventToolCalled         EventType = "tool.called"
	EventToolApprovalNeeded EventType = "tool.approval_needed"
	EventToolApproved       EventType = "tool.approved"
	EventToolDenied         EventType = "tool.denied"
	EventToolReturned       EventType = "tool.returned"
	EventToolFailed         EventType = "tool.failed"
	EventSubgraphStarted    EventType = "subgraph.started"
	EventSubgraphFinished   EventType = "subgraph.finished"
	EventSubgraphFailed     EventType = "subgraph.failed"
	EventCheckpointCreated  EventType = "checkpoint.created"
	EventArtifactCreated    EventType = "artifact.created"
	EventBreakpointHit      EventType = "breakpoint.hit"
	EventStateChanged       EventType = "state.changed"
	EventContractViolation  EventType = "contract.violation"
	EventWarning            EventType = "warning"
)

func IsStreamingEvent(event EventType) bool {
	return event == EventLLMContentChunk || event == EventLLMReasoningChunk
}

type RunRecord struct {
	RunID             string     `json:"run_id"`
	Revision          uint64     `json:"revision"`
	ParentRunID       string     `json:"parent_run_id,omitempty"`
	ParentStepID      string     `json:"parent_step_id,omitempty"`
	ParentTaskID      string     `json:"parent_task_id,omitempty"`
	RootRunID         string     `json:"root_run_id"`
	RunPath           []string   `json:"run_path"`
	Namespace         string     `json:"namespace"`
	ChildRunIDs       []string   `json:"child_run_ids,omitempty"`
	ReturnValue       any        `json:"return_value,omitempty"`
	GraphID           string     `json:"graph_id"`
	GraphVersion      string     `json:"graph_version"`
	GraphHash         string     `json:"graph_hash,omitempty"`
	GraphSnapshotHash string     `json:"graph_snapshot_hash,omitempty"`
	GraphSessionID    string     `json:"graph_session_id,omitempty"`
	Origin            *RunOrigin `json:"origin,omitempty"`
	Status            RunStatus  `json:"status"`
	EntryNodeID       string     `json:"entry_node_id"`
	CurrentNodeID     string     `json:"current_node_id,omitempty"`
	CurrentNodeIDs    []string   `json:"current_node_ids,omitempty"`
	CurrentStepIDs    []string   `json:"current_step_ids,omitempty"`
	NextNodeIDs       []string   `json:"next_node_ids,omitempty"`
	ParallelWaveID    string     `json:"parallel_wave_id,omitempty"`
	LastStepID        string     `json:"last_step_id,omitempty"`
	LastCheckpointID  string     `json:"last_checkpoint_id,omitempty"`
	PauseRequested    bool       `json:"pause_requested,omitempty"`
	CancelRequested   bool       `json:"cancel_requested,omitempty"`
	ErrorCode         string     `json:"error_code,omitempty"`
	ErrorMessage      string     `json:"error_message,omitempty"`
	StartedAt         time.Time  `json:"started_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	FinishedAt        *time.Time `json:"finished_at,omitempty"`
}

type RunOrigin struct {
	Type      string `json:"type"`
	TriggerID string `json:"trigger_id,omitempty"`
}

type ChildRunRequest struct {
	ParentRunID  string `json:"parent_run_id"`
	ParentStepID string `json:"parent_step_id"`
	ParentTaskID string `json:"parent_task_id"`
	GraphRef     string `json:"graph_ref"`
	Namespace    string `json:"namespace,omitempty"`
}

type ChildRunResult struct {
	Run         RunRecord    `json:"run"`
	State       *state.State `json:"-"`
	ReturnValue any          `json:"return_value,omitempty"`
	Interrupted bool         `json:"interrupted,omitempty"`
	Resumed     bool         `json:"resumed,omitempty"`
}

type ChildRunInterrupt struct {
	RunID        string `json:"run_id"`
	CheckpointID string `json:"checkpoint_id,omitempty"`
	Namespace    string `json:"namespace"`
	Status       string `json:"status"`
}

type StepRecord struct {
	StepID             string     `json:"step_id"`
	RunID              string     `json:"run_id"`
	TaskID             string     `json:"task_id"`
	ParentRunID        string     `json:"parent_run_id,omitempty"`
	ParentStepID       string     `json:"parent_step_id,omitempty"`
	ParentTaskID       string     `json:"parent_task_id,omitempty"`
	RootRunID          string     `json:"root_run_id,omitempty"`
	RunPath            []string   `json:"run_path,omitempty"`
	Namespace          string     `json:"namespace,omitempty"`
	NodeID             string     `json:"node_id"`
	NodeName           string     `json:"node_name"`
	WaveID             string     `json:"wave_id,omitempty"`
	Attempt            int        `json:"attempt"`
	Status             StepStatus `json:"status"`
	CheckpointBeforeID string     `json:"checkpoint_before_id,omitempty"`
	CheckpointAfterID  string     `json:"checkpoint_after_id,omitempty"`
	ErrorCode          string     `json:"error_code,omitempty"`
	ErrorMessage       string     `json:"error_message,omitempty"`
	StartedAt          time.Time  `json:"started_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	FinishedAt         *time.Time `json:"finished_at,omitempty"`
}

type CheckpointRecord struct {
	CheckpointID string          `json:"checkpoint_id"`
	RunID        string          `json:"run_id"`
	StepID       string          `json:"step_id"`
	TaskID       string          `json:"task_id,omitempty"`
	ParentRunID  string          `json:"parent_run_id,omitempty"`
	ParentStepID string          `json:"parent_step_id,omitempty"`
	ParentTaskID string          `json:"parent_task_id,omitempty"`
	RootRunID    string          `json:"root_run_id,omitempty"`
	RunPath      []string        `json:"run_path,omitempty"`
	Namespace    string          `json:"namespace,omitempty"`
	NodeID       string          `json:"node_id"`
	Stage        CheckpointStage `json:"stage"`
	StateCodec   string          `json:"state_codec"`
	StateVersion string          `json:"state_version"`
	PayloadRef   string          `json:"payload_ref"`
	CreatedAt    time.Time       `json:"created_at"`
}

type RestoredCheckpoint struct {
	Record    CheckpointRecord    `json:"record"`
	Snapshot  state.Snapshot      `json:"snapshot"`
	Business  *state.State        `json:"business"`
	Runtime   state.RuntimeState  `json:"runtime"`
	Artifacts []state.ArtifactRef `json:"artifacts,omitempty"`
}

type Artifact struct {
	ID           string    `json:"id,omitempty"`
	RunID        string    `json:"run_id,omitempty"`
	StepID       string    `json:"step_id,omitempty"`
	NodeID       string    `json:"node_id,omitempty"`
	ParentRunID  string    `json:"parent_run_id,omitempty"`
	ParentStepID string    `json:"parent_step_id,omitempty"`
	ParentTaskID string    `json:"parent_task_id,omitempty"`
	RootRunID    string    `json:"root_run_id,omitempty"`
	RunPath      []string  `json:"run_path,omitempty"`
	Namespace    string    `json:"namespace,omitempty"`
	Type         string    `json:"type,omitempty"`
	MIMEType     string    `json:"mime_type,omitempty"`
	Location     string    `json:"location,omitempty"`
	CreatedAt    time.Time `json:"created_at,omitempty"`
	Data         []byte    `json:"-"`
}

type Event struct {
	ID             string          `json:"id"`
	GraphID        string          `json:"graph_id"`
	GraphSessionID string          `json:"graph_session_id,omitempty"`
	RunID          string          `json:"run_id"`
	ParentRunID    string          `json:"parent_run_id,omitempty"`
	ParentStepID   string          `json:"parent_step_id,omitempty"`
	ParentTaskID   string          `json:"parent_task_id,omitempty"`
	RootRunID      string          `json:"root_run_id,omitempty"`
	RunPath        []string        `json:"run_path,omitempty"`
	Namespace      string          `json:"namespace,omitempty"`
	StepID         string          `json:"step_id,omitempty"`
	TaskID         string          `json:"task_id,omitempty"`
	NodeID         string          `json:"node_id,omitempty"`
	Type           EventType       `json:"type"`
	Timestamp      time.Time       `json:"timestamp"`
	Payload        json.RawMessage `json:"payload,omitempty"`
}

type WarningRecord struct {
	Code        string   `json:"code,omitempty"`
	NodeID      string   `json:"node_id,omitempty"`
	OtherNodeID string   `json:"other_node_id,omitempty"`
	Path        string   `json:"path,omitempty"`
	Sources     []string `json:"sources,omitempty"`
	Message     string   `json:"message"`
}

type RunFilter struct {
	Statuses     []RunStatus
	ParentRunID  string
	ParentTaskID string
	RootRunID    string
	Namespace    string
}

type Breakpoint struct {
	ID      string `json:"id"`
	NodeID  string `json:"node_id"`
	Stage   string `json:"stage"`
	Enabled bool   `json:"enabled"`
}

type ExecutionStore interface {
	CreateRun(ctx context.Context, run RunRecord) error
	CompareAndSwapRun(ctx context.Context, expectedRevision uint64, run RunRecord) (RunRecord, error)
	GetRun(ctx context.Context, runID string) (RunRecord, error)
	ListRuns(ctx context.Context, filter RunFilter) ([]RunRecord, error)
	AppendStep(ctx context.Context, step StepRecord) error
	UpdateStep(ctx context.Context, step StepRecord) error
	GetStep(ctx context.Context, stepID string) (StepRecord, error)
	ListSteps(ctx context.Context, runID string) ([]StepRecord, error)
}

func compareAndSwapRun(ctx context.Context, store ExecutionStore, run RunRecord) (RunRecord, error) {
	if store == nil {
		return RunRecord{}, errors.New("execution store is nil")
	}
	return store.CompareAndSwapRun(ctx, run.Revision, run)
}

type CheckpointStore interface {
	Save(ctx context.Context, record CheckpointRecord, payload []byte) error
	Load(ctx context.Context, checkpointID string) (CheckpointRecord, []byte, error)
	List(ctx context.Context, runID string) ([]CheckpointRecord, error)
}

type EventSink interface {
	Publish(ctx context.Context, event Event) error
	PublishBatch(ctx context.Context, events []Event) error
}

type ArtifactStore interface {
	Save(ctx context.Context, artifact Artifact) (state.ArtifactRef, error)
	Load(ctx context.Context, ref state.ArtifactRef) (Artifact, error)
	List(ctx context.Context, runID string) ([]state.ArtifactRef, error)
}
