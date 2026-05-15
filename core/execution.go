package core

import (
	"context"
	"time"
)

type ExecutableNode[S any, P any] interface {
	Execute(ctx context.Context, input S) (P, error)
}

type CheckpointStage string

const (
	CheckpointBeforeNode CheckpointStage = "before_node"
	CheckpointAfterNode  CheckpointStage = "after_node"
)

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

type WarningRecord struct {
	Code        string   `json:"code,omitempty"`
	NodeID      string   `json:"node_id,omitempty"`
	OtherNodeID string   `json:"other_node_id,omitempty"`
	Path        string   `json:"path,omitempty"`
	Sources     []string `json:"sources,omitempty"`
	Message     string   `json:"message"`
}
