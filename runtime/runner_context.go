package runtime

import (
	"context"
	"errors"

	"github.com/dengzii/weaveflow/state"
)

type runnerEventPublisher func(eventType EventType, payload any) error
type runnerEventFailureReporter func(context.Context, EventType, error)
type runnerArtifactRecorder func(ctx context.Context, artifact Artifact) (state.ArtifactRef, error)

type AgentInvocationRecorder interface {
	Start(context.Context, AgentInvocation) (AgentInvocation, error)
	Checkpoint(context.Context, AgentInvocation, string) (AgentInvocation, error)
	Finish(context.Context, AgentInvocation, error) error
}

// EventObserver synchronously observes fully populated events from one run.
type EventObserver interface {
	Observe(context.Context, Event) error
}

// EventObserverFunc adapts a function to EventObserver.
type EventObserverFunc func(context.Context, Event) error

func (f EventObserverFunc) Observe(ctx context.Context, event Event) error {
	return f(ctx, event)
}

type runnerEventPublisherKey struct{}
type runnerEventFailureReporterKey struct{}
type runnerMetadataKey struct{}
type runnerArtifactRecorderKey struct{}
type agentInvocationRecorderKey struct{}
type agentStateProviderKey struct{}
type agentResumePhaseKey struct{}
type runnerEventObserverKey struct{}
type runOriginKey struct{}
type graphExecutionBudgetProviderKey struct{}
type graphRunnerKey struct{}
type childRunLineageKey struct{}
type childRunControllerKey struct{}

var ErrArtifactRecorderUnavailable = errors.New("runner artifact recorder is unavailable")

var ErrInvalidEventCursor = errors.New("invalid event cursor")

type EventReader interface {
	ListEvents(runID string) ([]Event, error)
}

type EventPage struct {
	Items      []Event `json:"items"`
	NextCursor string  `json:"next_cursor"`
}

type EventPageReader interface {
	ListEventPage(runID, cursor string, limit int) (EventPage, error)
}

type RunnerMetadata struct {
	RunID        string   `json:"run_id,omitempty"`
	StepID       string   `json:"step_id,omitempty"`
	TaskID       string   `json:"task_id,omitempty"`
	NodeID       string   `json:"node_id,omitempty"`
	ParentRunID  string   `json:"parent_run_id,omitempty"`
	ParentStepID string   `json:"parent_step_id,omitempty"`
	ParentTaskID string   `json:"parent_task_id,omitempty"`
	RootRunID    string   `json:"root_run_id,omitempty"`
	RunPath      []string `json:"run_path,omitempty"`
	Namespace    string   `json:"namespace,omitempty"`
	Attempt      int      `json:"attempt,omitempty"`
}

type ChildRunLineage struct {
	ParentRunID   string
	ParentStepID  string
	ParentTaskID  string
	RootRunID     string
	ParentRunPath []string
	Namespace     string
}

type ChildRunController interface {
	RegisterChildRun(taskID string, runner *GraphRunner, runID string)
	UnregisterChildRun(taskID, runID string)
	ReserveChildRun(ctx context.Context, parentRunID string, pending PendingChildRun) (PendingChildRun, error)
	FinalizeChildRun(ctx context.Context, parentRunID, requestKey, childRunID string) error
}

func WithRunnerEventPublisher(ctx context.Context, publisher func(EventType, any) error) context.Context {
	if publisher == nil {
		if ctx == nil {
			return context.Background()
		}
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, runnerEventPublisherKey{}, runnerEventPublisher(publisher))
}

func withRunnerEventFailureReporter(ctx context.Context, reporter runnerEventFailureReporter) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if reporter == nil {
		return ctx
	}
	return context.WithValue(ctx, runnerEventFailureReporterKey{}, reporter)
}

func WithRunnerMetadata(ctx context.Context, metadata RunnerMetadata) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, runnerMetadataKey{}, metadata)
}

func WithGraphRunner(ctx context.Context, runner *GraphRunner) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, graphRunnerKey{}, runner)
}

func GraphRunnerFromContext(ctx context.Context) (*GraphRunner, bool) {
	if ctx == nil {
		return nil, false
	}
	runner, ok := ctx.Value(graphRunnerKey{}).(*GraphRunner)
	return runner, ok && runner != nil
}

func WithChildRunLineage(ctx context.Context, lineage ChildRunLineage) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	lineage.ParentRunPath = append([]string(nil), lineage.ParentRunPath...)
	return context.WithValue(ctx, childRunLineageKey{}, lineage)
}

func ChildRunLineageFromContext(ctx context.Context) (ChildRunLineage, bool) {
	if ctx == nil {
		return ChildRunLineage{}, false
	}
	lineage, ok := ctx.Value(childRunLineageKey{}).(ChildRunLineage)
	lineage.ParentRunPath = append([]string(nil), lineage.ParentRunPath...)
	return lineage, ok
}

func WithChildRunController(ctx context.Context, controller ChildRunController) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if controller == nil {
		return ctx
	}
	return context.WithValue(ctx, childRunControllerKey{}, controller)
}

func ChildRunControllerFromContext(ctx context.Context) (ChildRunController, bool) {
	if ctx == nil {
		return nil, false
	}
	controller, ok := ctx.Value(childRunControllerKey{}).(ChildRunController)
	return controller, ok && controller != nil
}

func WithRunOrigin(ctx context.Context, origin RunOrigin) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, runOriginKey{}, origin)
}

func RunOriginFromContext(ctx context.Context) (RunOrigin, bool) {
	if ctx == nil {
		return RunOrigin{}, false
	}
	origin, ok := ctx.Value(runOriginKey{}).(RunOrigin)
	return origin, ok
}

// WithRunnerEventObserver attaches a synchronous, run-scoped event observer.
// Errors from live events propagate; committed lifecycle events are observed
// best-effort because their transaction cannot be rolled back.
func WithRunnerEventObserver(ctx context.Context, observer EventObserver) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, runnerEventObserverKey{}, observer)
}

// RunnerEventObserverFromContext returns the run-scoped observer, if any.
func RunnerEventObserverFromContext(ctx context.Context) EventObserver {
	if ctx == nil {
		return nil
	}
	observer, _ := ctx.Value(runnerEventObserverKey{}).(EventObserver)
	return observer
}

func observeRunnerContextEvent(ctx context.Context, event Event) error {
	observer := RunnerEventObserverFromContext(ctx)
	if observer == nil {
		return nil
	}
	return observer.Observe(ctx, event)
}

func WithRunnerArtifactRecorder(ctx context.Context, recorder func(context.Context, Artifact) (state.ArtifactRef, error)) context.Context {
	if ctx == nil {
		return nil
	}
	if recorder == nil {
		return ctx
	}
	return context.WithValue(ctx, runnerArtifactRecorderKey{}, runnerArtifactRecorder(recorder))
}

func WithAgentInvocationRecorder(ctx context.Context, recorder AgentInvocationRecorder) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if recorder == nil {
		return ctx
	}
	return context.WithValue(ctx, agentInvocationRecorderKey{}, recorder)
}

func AgentInvocationRecorderFromContext(ctx context.Context) (AgentInvocationRecorder, bool) {
	if ctx == nil {
		return nil, false
	}
	recorder, ok := ctx.Value(agentInvocationRecorderKey{}).(AgentInvocationRecorder)
	return recorder, ok && recorder != nil
}

func WithAgentStateProvider(ctx context.Context, provider func() *state.State) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if provider == nil {
		return ctx
	}
	return context.WithValue(ctx, agentStateProviderKey{}, provider)
}

func AgentStateFromContext(ctx context.Context) (*state.State, bool) {
	if ctx == nil {
		return nil, false
	}
	provider, ok := ctx.Value(agentStateProviderKey{}).(func() *state.State)
	if !ok || provider == nil {
		return nil, false
	}
	return provider(), true
}

func WithAgentResumePhase(ctx context.Context, phase string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, agentResumePhaseKey{}, phase)
}

func AgentResumePhaseFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	phase, _ := ctx.Value(agentResumePhaseKey{}).(string)
	return phase
}

func PublishRunnerContextEvent(ctx context.Context, eventType EventType, payload any) error {
	if ctx == nil {
		return nil
	}
	publisher, _ := ctx.Value(runnerEventPublisherKey{}).(runnerEventPublisher)
	if publisher == nil {
		return nil
	}
	return publisher(eventType, payload)
}

func publishRunnerContextEventBestEffort(ctx context.Context, eventType EventType, payload any) {
	if err := PublishRunnerContextEvent(ctx, eventType, payload); err != nil {
		reporter, _ := ctx.Value(runnerEventFailureReporterKey{}).(runnerEventFailureReporter)
		if reporter != nil {
			reporter(ctx, eventType, err)
		}
	}
}

func HasRunnerEventPublisher(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	publisher, _ := ctx.Value(runnerEventPublisherKey{}).(runnerEventPublisher)
	return publisher != nil
}

func RunnerMetadataFromContext(ctx context.Context) (RunnerMetadata, bool) {
	if ctx == nil {
		return RunnerMetadata{}, false
	}
	metadata, ok := ctx.Value(runnerMetadataKey{}).(RunnerMetadata)
	metadata.RunPath = append([]string(nil), metadata.RunPath...)
	return metadata, ok
}

func WithGraphExecutionBudgetProvider(ctx context.Context, provider func() GraphExecutionBudget) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if provider == nil {
		return ctx
	}
	return context.WithValue(ctx, graphExecutionBudgetProviderKey{}, provider)
}

func GraphExecutionBudgetFromContext(ctx context.Context) (GraphExecutionBudget, bool) {
	if ctx == nil {
		return GraphExecutionBudget{}, false
	}
	provider, ok := ctx.Value(graphExecutionBudgetProviderKey{}).(func() GraphExecutionBudget)
	if !ok || provider == nil {
		return GraphExecutionBudget{}, false
	}
	return provider(), true
}

func SaveArtifact(ctx context.Context, artifact Artifact) (state.ArtifactRef, error) {
	if ctx == nil {
		return state.ArtifactRef{}, ErrArtifactRecorderUnavailable
	}
	artifact = sanitizeArtifact(ctx, artifact)
	recorder, _ := ctx.Value(runnerArtifactRecorderKey{}).(runnerArtifactRecorder)
	if recorder == nil {
		return state.ArtifactRef{}, ErrArtifactRecorderUnavailable
	}
	return recorder(ctx, artifact)
}

// HasArtifactRecorder reports whether the context carries an artifact recorder.
// Use it to skip building expensive payloads when no recorder will consume them.
func HasArtifactRecorder(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	recorder, _ := ctx.Value(runnerArtifactRecorderKey{}).(runnerArtifactRecorder)
	return recorder != nil
}

func SaveJSONArtifact(ctx context.Context, artifactType string, payload any) (state.ArtifactRef, error) {
	data, err := marshalRedactedJSON(ctx, payload)
	if err != nil {
		return state.ArtifactRef{}, err
	}
	return SaveArtifact(ctx, Artifact{
		Type:     artifactType,
		MIMEType: "application/json",
		Data:     data,
	})
}

func SaveArtifactBestEffort(ctx context.Context, artifact Artifact) (state.ArtifactRef, error) {
	ref, err := SaveArtifact(ctx, artifact)
	if errors.Is(err, ErrArtifactRecorderUnavailable) {
		return state.ArtifactRef{}, nil
	}
	return ref, err
}

func SaveJSONArtifactBestEffort(ctx context.Context, artifactType string, payload any) (state.ArtifactRef, error) {
	ref, err := SaveJSONArtifact(ctx, artifactType, payload)
	if errors.Is(err, ErrArtifactRecorderUnavailable) {
		return state.ArtifactRef{}, nil
	}
	return ref, err
}
