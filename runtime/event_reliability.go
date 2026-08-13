package runtime

import (
	"context"
	"time"

	"go.uber.org/zap"
)

type EventReliability string

const (
	EventReliabilityState      EventReliability = "state"
	EventReliabilityAudit      EventReliability = "audit"
	EventReliabilityLive       EventReliability = "live"
	EventReliabilityDiagnostic EventReliability = "diagnostic"
)

type EventPublicationFailure struct {
	Count          uint64    `json:"count"`
	LastError      string    `json:"last_error"`
	LastOccurredAt time.Time `json:"last_occurred_at"`
}

type EventPublicationDiagnostics struct {
	BestEffortFailures map[EventType]EventPublicationFailure `json:"best_effort_failures,omitempty"`
}

func EventReliabilityOf(eventType EventType) EventReliability {
	switch eventType {
	case EventRunCreated,
		EventRunStarted,
		EventRunPauseRequested,
		EventRunPaused,
		EventRunResumed,
		EventRunCancelRequested,
		EventRunCanceled,
		EventRunFinished,
		EventRunFailed,
		EventNodeStarted,
		EventNodeFinished,
		EventNodeFailed,
		EventNodeCanceled,
		EventNodeRetry,
		EventCheckpointCreated,
		EventBreakpointHit,
		EventStateChanged:
		return EventReliabilityState
	case EventLLMReasoningChunk, EventLLMContentChunk:
		return EventReliabilityLive
	case EventContractViolation, EventWarning:
		return EventReliabilityDiagnostic
	case EventNodeCustom,
		EventLLMReasoning,
		EventLLMContent,
		EventLLMFunctionCall,
		EventLLMUsage,
		EventLLMCall,
		EventToolStarted,
		EventToolCalled,
		EventToolReturned,
		EventToolFailed,
		EventSubgraphStarted,
		EventSubgraphFinished,
		EventSubgraphFailed,
		EventArtifactCreated:
		return EventReliabilityAudit
	default:
		return ""
	}
}

func RuntimeEventTypes() []EventType {
	return []EventType{
		EventRunCreated,
		EventRunStarted,
		EventRunPauseRequested,
		EventRunPaused,
		EventRunResumed,
		EventRunCancelRequested,
		EventRunCanceled,
		EventRunFinished,
		EventRunFailed,
		EventNodeStarted,
		EventNodeFinished,
		EventNodeFailed,
		EventNodeCanceled,
		EventNodeRetry,
		EventNodeCustom,
		EventLLMReasoningChunk,
		EventLLMContentChunk,
		EventLLMReasoning,
		EventLLMContent,
		EventLLMFunctionCall,
		EventLLMUsage,
		EventLLMCall,
		EventToolStarted,
		EventToolCalled,
		EventToolReturned,
		EventToolFailed,
		EventSubgraphStarted,
		EventSubgraphFinished,
		EventSubgraphFailed,
		EventCheckpointCreated,
		EventArtifactCreated,
		EventBreakpointHit,
		EventStateChanged,
		EventContractViolation,
		EventWarning,
	}
}

func (r *GraphRunner) EventPublicationDiagnostics() EventPublicationDiagnostics {
	if r == nil {
		return EventPublicationDiagnostics{}
	}
	r.eventDiagnosticsMu.Lock()
	defer r.eventDiagnosticsMu.Unlock()
	result := EventPublicationDiagnostics{
		BestEffortFailures: make(map[EventType]EventPublicationFailure, len(r.eventDiagnostics.BestEffortFailures)),
	}
	for eventType, failure := range r.eventDiagnostics.BestEffortFailures {
		result.BestEffortFailures[eventType] = failure
	}
	return result
}

func (r *GraphRunner) publishBestEffortEvent(
	ctx context.Context,
	run RunRecord,
	stepID string,
	nodeID string,
	eventType EventType,
	payload any,
) {
	metadata, _ := RunnerMetadataFromContext(ctx)
	if run.RunID == "" {
		run.RunID = metadata.RunID
	}
	if run.ParentRunID == "" {
		run.ParentRunID = metadata.ParentRunID
	}
	if run.ParentStepID == "" {
		run.ParentStepID = metadata.ParentStepID
	}
	if run.ParentTaskID == "" {
		run.ParentTaskID = metadata.ParentTaskID
	}
	if run.RootRunID == "" {
		run.RootRunID = metadata.RootRunID
	}
	if len(run.RunPath) == 0 {
		run.RunPath = append([]string(nil), metadata.RunPath...)
	}
	if run.Namespace == "" {
		run.Namespace = metadata.Namespace
	}
	if stepID == "" {
		stepID = metadata.StepID
	}
	if nodeID == "" {
		nodeID = metadata.NodeID
	}
	if err := r.publishEventWithTask(ctx, run, stepID, metadata.TaskID, nodeID, eventType, payload); err != nil {
		r.recordBestEffortEventFailure(eventType, err)
	}
}

func (r *GraphRunner) recordBestEffortEventFailure(eventType EventType, err error) {
	if r == nil || err == nil {
		return
	}
	r.eventDiagnosticsMu.Lock()
	if r.eventDiagnostics.BestEffortFailures == nil {
		r.eventDiagnostics.BestEffortFailures = map[EventType]EventPublicationFailure{}
	}
	failure := r.eventDiagnostics.BestEffortFailures[eventType]
	failure.Count++
	failure.LastError = err.Error()
	failure.LastOccurredAt = r.currentTime()
	r.eventDiagnostics.BestEffortFailures[eventType] = failure
	r.eventDiagnosticsMu.Unlock()
	logger.Warn("best-effort runtime event publication failed",
		zap.String("event_type", string(eventType)),
		zap.String("event_reliability", string(EventReliabilityOf(eventType))),
		zap.String("error", err.Error()),
	)
}
