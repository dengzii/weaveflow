package sqlite

import (
	"context"

	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

type Reader struct {
	path string
}

func OpenReader(path string) (*Reader, error) {
	store, err := Open(path)
	if err != nil {
		return nil, err
	}
	if err := store.Close(); err != nil {
		return nil, err
	}
	return &Reader{path: store.path}, nil
}

func (reader *Reader) ExecutionReader() fruntime.ExecutionReader {
	return executionReader{path: reader.path}
}
func (reader *Reader) CheckpointReader() fruntime.CheckpointReader {
	return checkpointReader{path: reader.path}
}
func (reader *Reader) ArtifactReader() fruntime.ArtifactReader {
	return artifactReader{path: reader.path}
}
func (reader *Reader) EventReader() fruntime.EventReader { return eventReader{path: reader.path} }
func (reader *Reader) EventPageReader() fruntime.EventPageReader {
	return eventReader{path: reader.path}
}

type executionReader struct{ path string }

func (reader executionReader) GetRun(ctx context.Context, runID string) (fruntime.RunRecord, error) {
	store, err := Open(reader.path)
	if err != nil {
		return fruntime.RunRecord{}, err
	}
	defer func() { _ = store.Close() }()
	return store.GetRun(ctx, runID)
}

func (reader executionReader) ListRuns(ctx context.Context, filter fruntime.RunFilter) ([]fruntime.RunRecord, error) {
	store, err := Open(reader.path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()
	return store.ListRuns(ctx, filter)
}

func (reader executionReader) GetStep(ctx context.Context, stepID string) (fruntime.StepRecord, error) {
	store, err := Open(reader.path)
	if err != nil {
		return fruntime.StepRecord{}, err
	}
	defer func() { _ = store.Close() }()
	return store.GetStep(ctx, stepID)
}

func (reader executionReader) ListSteps(ctx context.Context, runID string) ([]fruntime.StepRecord, error) {
	store, err := Open(reader.path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()
	return store.ListSteps(ctx, runID)
}

type checkpointReader struct{ path string }

func (reader checkpointReader) Load(ctx context.Context, checkpointID string) (fruntime.CheckpointRecord, []byte, error) {
	store, err := Open(reader.path)
	if err != nil {
		return fruntime.CheckpointRecord{}, nil, err
	}
	defer func() { _ = store.Close() }()
	return store.Load(ctx, checkpointID)
}

func (reader checkpointReader) List(ctx context.Context, runID string) ([]fruntime.CheckpointRecord, error) {
	store, err := Open(reader.path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()
	return store.List(ctx, runID)
}

type artifactReader struct{ path string }

func (reader artifactReader) Load(ctx context.Context, ref state.ArtifactRef) (fruntime.Artifact, error) {
	store, err := Open(reader.path)
	if err != nil {
		return fruntime.Artifact{}, err
	}
	defer func() { _ = store.Close() }()
	return store.ArtifactStore().Load(ctx, ref)
}

func (reader artifactReader) List(ctx context.Context, runID string) ([]state.ArtifactRef, error) {
	store, err := Open(reader.path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()
	return store.ArtifactStore().List(ctx, runID)
}

type eventReader struct{ path string }

func (reader eventReader) ListEvents(runID string) ([]fruntime.Event, error) {
	store, err := Open(reader.path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()
	return store.ListEvents(runID)
}

func (reader eventReader) ListEventPage(runID, cursor string, limit int) (fruntime.EventPage, error) {
	store, err := Open(reader.path)
	if err != nil {
		return fruntime.EventPage{}, err
	}
	defer func() { _ = store.Close() }()
	return store.ListEventPage(runID, cursor, limit)
}
