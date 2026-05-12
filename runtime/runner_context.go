package runtime

import (
	"context"
	"encoding/json"
	"errors"
	wfstate "weaveflow/state"
)

type runnerEventPublisher func(eventType EventType, payload any) error
type runnerArtifactRecorder func(ctx context.Context, artifact Artifact) (wfstate.ArtifactRef, error)

type runnerEventPublisherKey struct{}
type runnerMetadataKey struct{}
type runnerArtifactRecorderKey struct{}

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

func WithRunnerArtifactRecorder(ctx context.Context, recorder func(context.Context, Artifact) (wfstate.ArtifactRef, error)) context.Context {
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

func SaveArtifact(ctx context.Context, artifact Artifact) (wfstate.ArtifactRef, error) {
	if ctx == nil {
		return wfstate.ArtifactRef{}, ErrArtifactRecorderUnavailable
	}
	recorder, _ := ctx.Value(runnerArtifactRecorderKey{}).(runnerArtifactRecorder)
	if recorder == nil {
		return wfstate.ArtifactRef{}, ErrArtifactRecorderUnavailable
	}
	return recorder(ctx, artifact)
}

func SaveJSONArtifact(ctx context.Context, artifactType string, payload any) (wfstate.ArtifactRef, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return wfstate.ArtifactRef{}, err
	}
	return SaveArtifact(ctx, Artifact{
		Type:     artifactType,
		MIMEType: "application/json",
		Data:     data,
	})
}

func SaveArtifactBestEffort(ctx context.Context, artifact Artifact) (wfstate.ArtifactRef, error) {
	ref, err := SaveArtifact(ctx, artifact)
	if errors.Is(err, ErrArtifactRecorderUnavailable) {
		return wfstate.ArtifactRef{}, nil
	}
	return ref, err
}

func SaveJSONArtifactBestEffort(ctx context.Context, artifactType string, payload any) (wfstate.ArtifactRef, error) {
	ref, err := SaveJSONArtifact(ctx, artifactType, payload)
	if errors.Is(err, ErrArtifactRecorderUnavailable) {
		return wfstate.ArtifactRef{}, nil
	}
	return ref, err
}
