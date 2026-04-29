package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Runtime records, stores, and control primitives live in the runtime package.
// The root weaveflow package re-exports this surface as a compatibility facade.
var ErrRunnerRecordNotFound = errors.New("runner record not found")

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
)

type CheckpointStage string

const (
	CheckpointBeforeNode CheckpointStage = "before_node"
	CheckpointAfterNode  CheckpointStage = "after_node"
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
	EventNodeStarted        EventType = "nodes.started"
	EventNodeFinished       EventType = "nodes.finished"
	EventNodeFailed         EventType = "nodes.failed"
	EventNodeRetry          EventType = "nodes.retry"
	EventNodeCustom         EventType = "nodes.custom"
	EventLLMReasoningChunk  EventType = "llm.reasoning_chunk"
	EventLLMContentChunk    EventType = "llm.content_chunk"
	EventLLMReasoning       EventType = "llm.reasoning"
	EventLLMContent         EventType = "llm.content"
	EventLLMFunctionCall    EventType = "llm.function_call"
	EventLLMUsage           EventType = "llm.usage"
	EventToolStarted        EventType = "tool.started"
	EventToolCalled         EventType = "tool.called"
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
)

type RunRecord struct {
	RunID            string     `json:"run_id"`
	GraphID          string     `json:"graph_id"`
	GraphVersion     string     `json:"graph_version"`
	Status           RunStatus  `json:"status"`
	EntryNodeID      string     `json:"entry_node_id"`
	CurrentNodeID    string     `json:"current_node_id,omitempty"`
	LastStepID       string     `json:"last_step_id,omitempty"`
	LastCheckpointID string     `json:"last_checkpoint_id,omitempty"`
	PauseRequested   bool       `json:"pause_requested,omitempty"`
	CancelRequested  bool       `json:"cancel_requested,omitempty"`
	ErrorCode        string     `json:"error_code,omitempty"`
	ErrorMessage     string     `json:"error_message,omitempty"`
	StartedAt        time.Time  `json:"started_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
}

type StepRecord struct {
	StepID             string     `json:"step_id"`
	RunID              string     `json:"run_id"`
	NodeID             string     `json:"node_id"`
	NodeName           string     `json:"node_name"`
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
	NodeID       string          `json:"node_id"`
	Stage        CheckpointStage `json:"stage"`
	StateCodec   string          `json:"state_codec"`
	StateVersion string          `json:"state_version"`
	PayloadRef   string          `json:"payload_ref"`
	CreatedAt    time.Time       `json:"created_at"`
}

type RestoredCheckpoint struct {
	Record    CheckpointRecord `json:"record"`
	Snapshot  StateSnapshot    `json:"snapshot"`
	Business  State            `json:"business"`
	Runtime   RuntimeState     `json:"runtime"`
	Artifacts []ArtifactRef    `json:"artifacts,omitempty"`
}

type Artifact struct {
	ID        string    `json:"id,omitempty"`
	RunID     string    `json:"run_id,omitempty"`
	StepID    string    `json:"step_id,omitempty"`
	NodeID    string    `json:"node_id,omitempty"`
	Type      string    `json:"type,omitempty"`
	MIMEType  string    `json:"mime_type,omitempty"`
	Location  string    `json:"location,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	Data      []byte    `json:"-"`
}

type Event struct {
	ID        string          `json:"id"`
	RunID     string          `json:"run_id"`
	StepID    string          `json:"step_id,omitempty"`
	NodeID    string          `json:"node_id,omitempty"`
	Type      EventType       `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

type RunFilter struct {
	Statuses []RunStatus
}

type Breakpoint struct {
	ID      string `json:"id"`
	NodeID  string `json:"node_id"`
	Stage   string `json:"stage"`
	Enabled bool   `json:"enabled"`
}

type BreakpointHit struct {
	BreakpointID string    `json:"breakpoint_id"`
	NodeID       string    `json:"node_id"`
	Stage        string    `json:"stage"`
	HitAt        time.Time `json:"hit_at"`
}

type ExecutionStore interface {
	CreateRun(ctx context.Context, run RunRecord) error
	UpdateRun(ctx context.Context, run RunRecord) error
	GetRun(ctx context.Context, runID string) (RunRecord, error)
	ListRuns(ctx context.Context, filter RunFilter) ([]RunRecord, error)
	AppendStep(ctx context.Context, step StepRecord) error
	UpdateStep(ctx context.Context, step StepRecord) error
	GetStep(ctx context.Context, stepID string) (StepRecord, error)
	ListSteps(ctx context.Context, runID string) ([]StepRecord, error)
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
	Save(ctx context.Context, artifact Artifact) (ArtifactRef, error)
	Load(ctx context.Context, ref ArtifactRef) (Artifact, error)
	List(ctx context.Context, runID string) ([]ArtifactRef, error)
}
