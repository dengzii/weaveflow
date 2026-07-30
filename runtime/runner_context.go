package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/dengzii/weaveflow/state"
)

type runnerEventPublisher func(eventType EventType, payload any) error
type runnerArtifactRecorder func(ctx context.Context, artifact Artifact) (state.ArtifactRef, error)

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
type runnerMetadataKey struct{}
type runnerArtifactRecorderKey struct{}
type runnerEventObserverKey struct{}

var ErrArtifactRecorderUnavailable = errors.New("runner artifact recorder is unavailable")

type EventReader interface {
	ListEvents(runID string) ([]Event, error)
}

type RunnerMetadata struct {
	RunID   string `json:"run_id,omitempty"`
	StepID  string `json:"step_id,omitempty"`
	NodeID  string `json:"node_id,omitempty"`
	Attempt int    `json:"attempt,omitempty"`
}

func WithRunnerEventPublisher(ctx context.Context, publisher func(EventType, any) error) context.Context {
	if publisher == nil {
		return ctx
	}
	return context.WithValue(ctx, runnerEventPublisherKey{}, runnerEventPublisher(publisher))
}

func WithRunnerMetadata(ctx context.Context, metadata RunnerMetadata) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, runnerMetadataKey{}, metadata)
}

// WithRunnerEventObserver attaches a synchronous, run-scoped event observer.
// Observer errors propagate through the operation that published the event.
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
	return metadata, ok
}

func SaveArtifact(ctx context.Context, artifact Artifact) (state.ArtifactRef, error) {
	if ctx == nil {
		return state.ArtifactRef{}, ErrArtifactRecorderUnavailable
	}
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
	data, err := json.Marshal(payload)
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
