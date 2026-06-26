package core

import "github.com/dengzii/weaveflow/state"

type CheckpointStage string

const (
	CheckpointBeforeNode        CheckpointStage = "before_node"
	CheckpointAfterNode         CheckpointStage = "after_node"
	CheckpointAfterParallelWave CheckpointStage = "after_parallel_wave"
)

type Breakpoint struct {
	ID      string `json:"id"`
	NodeID  string `json:"node_id"`
	Stage   string `json:"stage"`
	Enabled bool   `json:"enabled"`
}

type BreakpointHit = state.BreakpointHit

type WarningRecord struct {
	Code        string   `json:"code,omitempty"`
	NodeID      string   `json:"node_id,omitempty"`
	OtherNodeID string   `json:"other_node_id,omitempty"`
	Path        string   `json:"path,omitempty"`
	Sources     []string `json:"sources,omitempty"`
	Message     string   `json:"message"`
}
