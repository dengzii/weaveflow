package runtime

import (
	"encoding/json"
	"time"
)

const DefaultStateVersion = "falcon.state.v2"

type GraphState map[string]json.RawMessage

type StateSnapshot struct {
	Version      string                `json:"version"`
	Runtime      RuntimeState          `json:"runtime"`
	Conversation *ConversationState    `json:"conversation,omitempty"`
	Shared       GraphState            `json:"shared,omitempty"`
	Scopes       map[string]GraphState `json:"scopes,omitempty"`
	Artifacts    []ArtifactRef         `json:"artifacts,omitempty"`
}

// RestoredStateSnapshot separates the runtime envelope from the business state
// reconstructed for graph execution.
type RestoredStateSnapshot struct {
	Snapshot  StateSnapshot `json:"snapshot"`
	Business  State         `json:"business"`
	Runtime   RuntimeState  `json:"runtime"`
	Artifacts []ArtifactRef `json:"artifacts,omitempty"`
}

type RuntimeState struct {
	RunID           string         `json:"run_id,omitempty"`
	CurrentStepID   string         `json:"current_step_id,omitempty"`
	CurrentNodeID   string         `json:"current_node_id,omitempty"`
	Status          string         `json:"status,omitempty"`
	RetryCount      int            `json:"retry_count,omitempty"`
	PauseRequested  bool           `json:"pause_requested,omitempty"`
	CancelRequested bool           `json:"cancel_requested,omitempty"`
	BreakpointHit   *BreakpointHit `json:"breakpoint_hit,omitempty"`
}

type ConversationState struct {
	Messages       []StateMessage `json:"messages,omitempty"`
	FinalAnswer    string         `json:"final_answer,omitempty"`
	IterationCount int            `json:"iteration_count,omitempty"`
	MaxIterations  int            `json:"max_iterations,omitempty"`
}

type ArtifactRef struct {
	ID        string    `json:"id"`
	RunID     string    `json:"run_id,omitempty"`
	StepID    string    `json:"step_id,omitempty"`
	NodeID    string    `json:"node_id,omitempty"`
	Type      string    `json:"type,omitempty"`
	MIMEType  string    `json:"mime_type,omitempty"`
	Location  string    `json:"location,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

type StateMessage struct {
	Role  string             `json:"role"`
	Parts []StateMessagePart `json:"parts,omitempty"`
}

type StateMessagePart struct {
	Kind         string `json:"kind"`
	Text         string `json:"text,omitempty"`
	URL          string `json:"url,omitempty"`
	Detail       string `json:"detail,omitempty"`
	MIMEType     string `json:"mime_type,omitempty"`
	Data         string `json:"data,omitempty"`
	ToolCallID   string `json:"tool_call_id,omitempty"`
	ToolType     string `json:"tool_type,omitempty"`
	FunctionName string `json:"function_name,omitempty"`
	Arguments    string `json:"arguments,omitempty"`
	Name         string `json:"name,omitempty"`
	Content      string `json:"content,omitempty"`
}

type StateChange struct {
	Path   string          `json:"path"`
	Before json.RawMessage `json:"before,omitempty"`
	After  json.RawMessage `json:"after,omitempty"`
}

type StateCodec interface {
	Name() string
	Version() string
	Encode(snapshot StateSnapshot) ([]byte, error)
	Decode(data []byte) (StateSnapshot, error)
	Diff(before, after StateSnapshot) ([]StateChange, error)
}
