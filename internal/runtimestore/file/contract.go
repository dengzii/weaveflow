package file

import (
	"context"

	fruntime "github.com/dengzii/weaveflow/runtime"
)

type Artifact = fruntime.Artifact
type ArtifactStage = fruntime.ArtifactStage
type CheckpointRecord = fruntime.CheckpointRecord
type Commit = fruntime.Commit
type CommitResult = fruntime.CommitResult
type Event = fruntime.Event
type EventPage = fruntime.EventPage
type EventPageReader = fruntime.EventPageReader
type EventReader = fruntime.EventReader
type EventSink = fruntime.EventSink
type ExecutionReader = fruntime.ExecutionReader
type ExecutionStore = fruntime.ExecutionStore
type CheckpointReader = fruntime.CheckpointReader
type CheckpointStore = fruntime.CheckpointStore
type ArtifactReader = fruntime.ArtifactReader
type ArtifactStore = fruntime.ArtifactStore
type RetentionAuditRecord = fruntime.RetentionAuditRecord
type RunFilter = fruntime.RunFilter
type RunRecord = fruntime.RunRecord
type RunRevisionConflictError = fruntime.RunRevisionConflictError
type RunStatus = fruntime.RunStatus
type StepRecord = fruntime.StepRecord
type TransactionStore = fruntime.TransactionStore
type RunDeleter = fruntime.RunDeleter
type RunDeletionExecutionStore = fruntime.RunDeletionExecutionStore
type RunDeletionManifest = fruntime.RunDeletionManifest
type RunDeletionManifestStore = fruntime.RunDeletionManifestStore
type RunDeletionFenceScanner = fruntime.RunDeletionFenceScanner

const (
	EventLLMContentChunk   = fruntime.EventLLMContentChunk
	EventLLMReasoningChunk = fruntime.EventLLMReasoningChunk
	RunWriteCheck          = fruntime.RunWriteCheck
	RunWriteCreate         = fruntime.RunWriteCreate
	RunWriteUpdate         = fruntime.RunWriteUpdate
	StepWriteAppend        = fruntime.StepWriteAppend
	StepWriteUpdate        = fruntime.StepWriteUpdate
)

var ErrInvalidEventCursor = fruntime.ErrInvalidEventCursor
var ErrRunControlNotAllowed = fruntime.ErrRunControlNotAllowed
var ErrRunnerRecordNotFound = fruntime.ErrRunnerRecordNotFound

func IsStreamingEvent(eventType fruntime.EventType) bool {
	return fruntime.IsStreamingEvent(eventType)
}

func cloneEvent(event Event) Event {
	return fruntime.CloneEvent(event)
}

func cloneRunRecord(run RunRecord) RunRecord {
	return fruntime.CloneRunRecord(run)
}

func cloneStepRecord(step StepRecord) StepRecord {
	return fruntime.CloneStepRecord(step)
}

func ensureRunNotDeleting(run RunRecord, action string) error {
	return fruntime.EnsureRunNotDeleting(run, action)
}

func sanitizeArtifact(ctx context.Context, artifact Artifact) Artifact {
	return fruntime.SanitizeArtifact(ctx, artifact)
}

func sanitizeCommit(ctx context.Context, commit Commit) Commit {
	return fruntime.SanitizeCommit(ctx, commit)
}

func sanitizeEventPayload(ctx context.Context, event Event) Event {
	return fruntime.SanitizeEvent(ctx, event)
}

func sanitizeEvents(ctx context.Context, events []Event) []Event {
	return fruntime.SanitizeEvents(ctx, events)
}

func sanitizeRunRecord(ctx context.Context, run RunRecord) RunRecord {
	return fruntime.SanitizeRunRecord(ctx, run)
}

func sanitizeStepRecord(ctx context.Context, step StepRecord) StepRecord {
	return fruntime.SanitizeStepRecord(ctx, step)
}

func validateNewRunDeletion(run RunRecord) error {
	return fruntime.ValidateNewRunDeletion(run)
}

func validateNewRunParent(run, parent RunRecord) error {
	return fruntime.ValidateNewRunParent(run, parent)
}

func validateRunChildState(run RunRecord) error {
	return fruntime.ValidateRunChildState(run)
}

func validateRunDeletionState(deletion *fruntime.RunDeletionState) error {
	return fruntime.ValidateRunDeletionState(deletion)
}

func validateRunDeletionTransition(ctx context.Context, existing, next RunRecord) error {
	return fruntime.ValidateRunDeletionTransition(ctx, existing, next)
}

func validateRunExecutionLeaseTransition(ctx context.Context, existing, next RunRecord) error {
	return fruntime.ValidateRunExecutionLeaseTransition(ctx, existing, next)
}

func validateStepEffectTransition(existing, next StepRecord) error {
	return fruntime.ValidateStepEffectTransition(existing, next)
}

func validateStepEffect(step StepRecord) error {
	return fruntime.ValidateStepEffect(step)
}

func validateCommitExecutionLease(run RunRecord, commit Commit) error {
	return fruntime.ValidateCommitExecutionLease(run, commit)
}

func fruntimeExecutionLeasesEqual(existing, next RunRecord) bool {
	return fruntime.ExecutionLeasesEqual(existing.ExecutionLease, next.ExecutionLease)
}

func validateRuntimeCommit(commit Commit) error {
	return fruntime.ValidateCommit(commit)
}

func requireRuntimeRunDeletionMutation(ctx context.Context, runID, deletionID string) error {
	return fruntime.RequireRunDeletionMutation(ctx, runID, deletionID)
}
