package falcon

import (
	"context"

	fruntime "falcon/runtime"
	"github.com/tmc/langchaingo/llms"
)

const (
	CommonStateSchemaID = fruntime.CommonStateSchemaID
	DefaultStateVersion = fruntime.DefaultStateVersion

	StateKeyMessages       = fruntime.StateKeyMessages
	StateKeyIterationCount = fruntime.StateKeyIterationCount
	StateKeyMaxIterations  = fruntime.StateKeyMaxIterations
	StateKeyFinalAnswer    = fruntime.StateKeyFinalAnswer

	StateNamespacePrefix = fruntime.StateNamespacePrefix
)

var (
	ErrRunnerRecordNotFound        = fruntime.ErrRunnerRecordNotFound
	ErrArtifactRecorderUnavailable = fruntime.ErrArtifactRecorderUnavailable
)

type (
	State                 = fruntime.State
	ConversationFacet     = fruntime.ConversationFacet
	GraphState            = fruntime.GraphState
	StateSnapshot         = fruntime.StateSnapshot
	RestoredStateSnapshot = fruntime.RestoredStateSnapshot
	RuntimeState          = fruntime.RuntimeState
	ConversationState     = fruntime.ConversationState
	ArtifactRef           = fruntime.ArtifactRef
	StateMessage          = fruntime.StateMessage
	StateMessagePart      = fruntime.StateMessagePart
	StateChange           = fruntime.StateChange
	StateCodec            = fruntime.StateCodec
	JSONStateCodec        = fruntime.JSONStateCodec

	RunStatus          = fruntime.RunStatus
	StepStatus         = fruntime.StepStatus
	CheckpointStage    = fruntime.CheckpointStage
	EventType          = fruntime.EventType
	RunRecord          = fruntime.RunRecord
	StepRecord         = fruntime.StepRecord
	CheckpointRecord   = fruntime.CheckpointRecord
	RestoredCheckpoint = fruntime.RestoredCheckpoint
	Artifact           = fruntime.Artifact
	Event              = fruntime.Event
	RunFilter          = fruntime.RunFilter
	Breakpoint         = fruntime.Breakpoint
	BreakpointHit      = fruntime.BreakpointHit
	ExecutionStore     = fruntime.ExecutionStore
	CheckpointStore    = fruntime.CheckpointStore
	EventSink          = fruntime.EventSink
	ArtifactStore      = fruntime.ArtifactStore
	EventReader        = fruntime.EventReader
	RunnerMetadata     = fruntime.RunnerMetadata

	FileArtifactStore   = fruntime.FileArtifactStore
	FileExecutionStore  = fruntime.FileExecutionStore
	FileCheckpointStore = fruntime.FileCheckpointStore
	FileEventSink       = fruntime.FileEventSink
)

func defaultStateFieldDefinitions() []StateFieldDefinition {
	defs := fruntime.DefaultStateFieldDefinitions()
	result := make([]StateFieldDefinition, 0, len(defs))
	for _, def := range defs {
		result = append(result, StateFieldDefinition{
			Name:        def.Name,
			Description: def.Description,
			Schema:      cloneJSONSchema(def.Schema),
		})
	}
	return result
}

func cloneJSONSchema(input map[string]any) JSONSchema {
	if len(input) == 0 {
		return nil
	}
	cloned := make(JSONSchema, len(input))
	for key, value := range input {
		cloned[key] = cloneJSONSchemaValue(value)
	}
	return cloned
}

func cloneJSONSchemaValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return map[string]any(cloneJSONSchema(typed))
	case []any:
		cloned := make([]any, len(typed))
		for i, item := range typed {
			cloned[i] = cloneJSONSchemaValue(item)
		}
		return cloned
	default:
		return value
	}
}

const (
	RunStatusPending   = fruntime.RunStatusPending
	RunStatusRunning   = fruntime.RunStatusRunning
	RunStatusPaused    = fruntime.RunStatusPaused
	RunStatusFailed    = fruntime.RunStatusFailed
	RunStatusCompleted = fruntime.RunStatusCompleted
	RunStatusCanceled  = fruntime.RunStatusCanceled

	StepStatusScheduled = fruntime.StepStatusScheduled
	StepStatusRunning   = fruntime.StepStatusRunning
	StepStatusSucceeded = fruntime.StepStatusSucceeded
	StepStatusFailed    = fruntime.StepStatusFailed
	StepStatusPaused    = fruntime.StepStatusPaused

	CheckpointBeforeNode = fruntime.CheckpointBeforeNode
	CheckpointAfterNode  = fruntime.CheckpointAfterNode

	EventRunCreated         = fruntime.EventRunCreated
	EventRunStarted         = fruntime.EventRunStarted
	EventRunPauseRequested  = fruntime.EventRunPauseRequested
	EventRunPaused          = fruntime.EventRunPaused
	EventRunResumed         = fruntime.EventRunResumed
	EventRunCancelRequested = fruntime.EventRunCancelRequested
	EventRunCanceled        = fruntime.EventRunCanceled
	EventRunFinished        = fruntime.EventRunFinished
	EventRunFailed          = fruntime.EventRunFailed
	EventNodeStarted        = fruntime.EventNodeStarted
	EventNodeFinished       = fruntime.EventNodeFinished
	EventNodeFailed         = fruntime.EventNodeFailed
	EventNodeRetry          = fruntime.EventNodeRetry
	EventLLMReasoningChunk  = fruntime.EventLLMReasoningChunk
	EventLLMContentChunk    = fruntime.EventLLMContentChunk
	EventToolCalled         = fruntime.EventToolCalled
	EventToolReturned       = fruntime.EventToolReturned
	EventToolFailed         = fruntime.EventToolFailed
	EventCheckpointCreated  = fruntime.EventCheckpointCreated
	EventArtifactCreated    = fruntime.EventArtifactCreated
	EventBreakpointHit      = fruntime.EventBreakpointHit
	EventStateChanged       = fruntime.EventStateChanged
)

func NewBaseState(messages []llms.MessageContent, maxIterations int) State {
	return fruntime.NewBaseState(messages, maxIterations)
}

func Conversation(state State, scope string) ConversationFacet {
	return fruntime.Conversation(state, scope)
}

func NewJSONStateCodec(version string) *JSONStateCodec {
	return fruntime.NewJSONStateCodec(version)
}

func SnapshotFromState(state State) (StateSnapshot, error) {
	return fruntime.SnapshotFromState(state)
}

func SnapshotFromStateWithRuntime(state State, runtime RuntimeState, artifacts []ArtifactRef) (StateSnapshot, error) {
	return fruntime.SnapshotFromStateWithRuntime(state, runtime, artifacts)
}

func RestoreStateSnapshot(snapshot StateSnapshot) (RestoredStateSnapshot, error) {
	return fruntime.RestoreStateSnapshot(snapshot)
}

func StateFromSnapshot(snapshot StateSnapshot) (State, error) {
	return fruntime.StateFromSnapshot(snapshot)
}

func NewFileArtifactStore(baseDir string) *FileArtifactStore {
	return fruntime.NewFileArtifactStore(baseDir)
}

func NewFileExecutionStore(baseDir string) *FileExecutionStore {
	return fruntime.NewFileExecutionStore(baseDir)
}

func NewFileCheckpointStore(baseDir string) *FileCheckpointStore {
	return fruntime.NewFileCheckpointStore(baseDir)
}

func NewFileEventSink(baseDir string) *FileEventSink {
	return fruntime.NewFileEventSink(baseDir)
}

func RunnerMetadataFromContext(ctx context.Context) (RunnerMetadata, bool) {
	return fruntime.RunnerMetadataFromContext(ctx)
}

func SaveArtifact(ctx context.Context, artifact Artifact) (ArtifactRef, error) {
	return fruntime.SaveArtifact(ctx, artifact)
}

func SaveJSONArtifact(ctx context.Context, artifactType string, payload any) (ArtifactRef, error) {
	return fruntime.SaveJSONArtifact(ctx, artifactType, payload)
}

func NormalizeStateNamespace(namespace string) string {
	return fruntime.NormalizeStateNamespace(namespace)
}
