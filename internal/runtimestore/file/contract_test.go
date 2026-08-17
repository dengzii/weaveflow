package file

import (
	"context"

	fruntime "github.com/dengzii/weaveflow/runtime"
)

type RunDeletionState = fruntime.RunDeletionState

const (
	EventLLMReasoning   = fruntime.EventLLMReasoning
	EventRunCreated     = fruntime.EventRunCreated
	EventRunFailed      = fruntime.EventRunFailed
	EventRunStarted     = fruntime.EventRunStarted
	RunDeletionReserved = fruntime.RunDeletionReserved
	RunStatusCompleted  = fruntime.RunStatusCompleted
)

func withRunDeletionMutation(ctx context.Context, deletionID string) context.Context {
	return fruntime.WithRunDeletionMutation(ctx, deletionID)
}
