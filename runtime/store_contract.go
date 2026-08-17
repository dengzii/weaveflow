package runtime

import "context"

func ValidateStorageID(name, value string) error {
	return validateRunnerStorageID(name, value)
}

func ValidateCommit(commit Commit) error {
	return validateRuntimeCommit(commit)
}

func ValidateNewRunDeletion(run RunRecord) error {
	return validateNewRunDeletion(run)
}

func ValidateNewRunParent(run, parent RunRecord) error {
	return validateNewRunParent(run, parent)
}

func ValidateRunChildState(run RunRecord) error {
	return validateRunChildState(run)
}

func ValidateRunDeletionState(deletion *RunDeletionState) error {
	return validateRunDeletionState(deletion)
}

func ValidateRunDeletionManifest(manifest RunDeletionManifest) error {
	return validateRunDeletionManifest(manifest)
}

func ValidateRunDeletionTransition(ctx context.Context, existing, next RunRecord) error {
	return validateRunDeletionTransition(ctx, existing, next)
}

func EnsureRunNotDeleting(run RunRecord, action string) error {
	return ensureRunNotDeleting(run, action)
}

func WithRunDeletionMutation(ctx context.Context, deletionID string) context.Context {
	return withRunDeletionMutation(ctx, deletionID)
}

func SanitizeArtifact(ctx context.Context, artifact Artifact) Artifact {
	return sanitizeArtifact(ctx, artifact)
}

func SanitizeCommit(ctx context.Context, commit Commit) Commit {
	return sanitizeCommit(ctx, commit)
}

func SanitizeEvent(ctx context.Context, event Event) Event {
	return sanitizeEventPayload(ctx, event)
}

func SanitizeEvents(ctx context.Context, events []Event) []Event {
	return sanitizeEvents(ctx, events)
}

func SanitizeRunRecord(ctx context.Context, run RunRecord) RunRecord {
	return sanitizeRunRecord(ctx, run)
}

func SanitizeStepRecord(ctx context.Context, step StepRecord) StepRecord {
	return sanitizeStepRecord(ctx, step)
}

func CloneEvent(event Event) Event {
	return cloneEvent(event)
}

func CloneRunRecord(run RunRecord) RunRecord {
	return cloneRunRecord(run)
}

func CloneStepRecord(step StepRecord) StepRecord {
	return cloneStepRecord(step)
}
