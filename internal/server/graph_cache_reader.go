package server

import (
	"context"
	"errors"
	"sort"

	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

func (r *combinedRunReader) ListRuns(ctx context.Context, filter runtime.RunFilter) ([]runtime.RunRecord, error) {
	byID := make(map[string]runtime.RunRecord)
	for _, reader := range r.readers {
		runs, err := reader.ListRuns(ctx, filter)
		if err != nil {
			return nil, err
		}
		for _, run := range runs {
			if _, exists := byID[run.RunID]; !exists {
				byID[run.RunID] = run
			}
		}
	}
	all := make([]runtime.RunRecord, 0, len(byID))
	for _, run := range byID {
		all = append(all, run)
	}
	sortRunsByStart(all)
	return all, nil
}

func (r *combinedRunReader) GetRun(ctx context.Context, runID string) (runtime.RunRecord, error) {
	_, run, err := r.readerForRun(ctx, runID)
	return run, err
}

func (r *combinedRunReader) readerForRun(ctx context.Context, runID string) (runReader, runtime.RunRecord, error) {
	for _, reader := range r.readers {
		run, err := reader.GetRun(ctx, runID)
		if err == nil {
			return reader, run, nil
		}
		if !errors.Is(err, runtime.ErrRunnerRecordNotFound) {
			return nil, runtime.RunRecord{}, err
		}
	}
	return nil, runtime.RunRecord{}, runtime.ErrRunnerRecordNotFound
}

func (r *combinedRunReader) ListSteps(ctx context.Context, runID string) ([]runtime.StepRecord, error) {
	reader, _, err := r.readerForRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	return reader.ListSteps(ctx, runID)
}

func (r *combinedRunReader) ListCheckpoints(ctx context.Context, runID string) ([]runtime.CheckpointRecord, error) {
	reader, _, err := r.readerForRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	return reader.ListCheckpoints(ctx, runID)
}

func (r *combinedRunReader) LoadCheckpointState(ctx context.Context, checkpointID string) (runtime.RestoredCheckpoint, error) {
	for _, reader := range r.readers {
		checkpoint, err := reader.LoadCheckpointState(ctx, checkpointID)
		if err == nil {
			return checkpoint, nil
		}
		if !errors.Is(err, runtime.ErrRunnerRecordNotFound) {
			return runtime.RestoredCheckpoint{}, err
		}
	}
	return runtime.RestoredCheckpoint{}, runtime.ErrRunnerRecordNotFound
}

func (r *combinedRunReader) ListEvents(runID string) ([]runtime.Event, error) {
	reader, _, err := r.readerForRun(context.Background(), runID)
	if err != nil {
		if !errors.Is(err, runtime.ErrRunnerRecordNotFound) {
			return nil, err
		}
		for _, candidate := range r.readers {
			events, listErr := candidate.ListEvents(runID)
			if listErr == nil && len(events) > 0 {
				return events, nil
			}
			if listErr != nil && !errors.Is(listErr, runtime.ErrRunnerRecordNotFound) {
				return nil, listErr
			}
		}
		return nil, err
	}
	return reader.ListEvents(runID)
}

func (r *combinedRunReader) ListEventPage(runID, cursor string, limit int) (runtime.EventPage, error) {
	reader, _, err := r.readerForRun(context.Background(), runID)
	if err != nil {
		if !errors.Is(err, runtime.ErrRunnerRecordNotFound) {
			return runtime.EventPage{}, err
		}
		for _, candidate := range r.readers {
			page, listErr := candidate.ListEventPage(runID, cursor, limit)
			if listErr == nil && (len(page.Items) > 0 || page.NextCursor != "") {
				return page, nil
			}
			if listErr != nil && !errors.Is(listErr, runtime.ErrRunnerRecordNotFound) {
				return runtime.EventPage{}, listErr
			}
		}
		return runtime.EventPage{}, err
	}
	return reader.ListEventPage(runID, cursor, limit)
}

func (r *combinedRunReader) ListArtifacts(ctx context.Context, runID string) ([]state.ArtifactRef, error) {
	reader, _, err := r.readerForRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	return reader.ListArtifacts(ctx, runID)
}

func (r *combinedRunReader) LoadArtifact(ctx context.Context, ref state.ArtifactRef) (runtime.Artifact, error) {
	reader, _, err := r.readerForRun(ctx, ref.RunID)
	if err != nil {
		return runtime.Artifact{}, err
	}
	return reader.LoadArtifact(ctx, ref)
}

func (r *graphCacheReader) ListRuns(ctx context.Context, filter runtime.RunFilter) ([]runtime.RunRecord, error) {
	all := make([]runtime.RunRecord, 0)
	for _, store := range r.executionStores {
		runs, err := store.ListRuns(ctx, filter)
		if err != nil {
			return nil, err
		}
		all = append(all, runs...)
	}
	sortRunsByStart(all)
	return all, nil
}

func sortRunsByStart(runs []runtime.RunRecord) {
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].StartedAt.Equal(runs[j].StartedAt) {
			return runs[i].RunID < runs[j].RunID
		}
		return runs[i].StartedAt.Before(runs[j].StartedAt)
	})
}

func (r *graphCacheReader) GetRun(ctx context.Context, runID string) (runtime.RunRecord, error) {
	_, run, err := r.locateRun(ctx, runID)
	return run, err
}

func (r *graphCacheReader) locateRun(ctx context.Context, runID string) (int, runtime.RunRecord, error) {
	for index, store := range r.executionStores {
		run, err := store.GetRun(ctx, runID)
		if err == nil {
			return index, run, nil
		}
		if !errors.Is(err, runtime.ErrRunnerRecordNotFound) {
			return -1, runtime.RunRecord{}, err
		}
	}
	return -1, runtime.RunRecord{}, runtime.ErrRunnerRecordNotFound
}

func (r *graphCacheReader) ListSteps(ctx context.Context, runID string) ([]runtime.StepRecord, error) {
	index, _, err := r.locateRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if index >= len(r.executionStores) {
		return nil, runtime.ErrRunnerRecordNotFound
	}
	return r.executionStores[index].ListSteps(ctx, runID)
}

func (r *graphCacheReader) ListCheckpoints(ctx context.Context, runID string) ([]runtime.CheckpointRecord, error) {
	index, _, err := r.locateRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if index >= len(r.checkpointStores) {
		return nil, runtime.ErrRunnerRecordNotFound
	}
	return r.checkpointStores[index].List(ctx, runID)
}

func (r *graphCacheReader) LoadCheckpointState(ctx context.Context, checkpointID string) (runtime.RestoredCheckpoint, error) {
	for _, store := range r.checkpointStores {
		record, payload, err := store.Load(ctx, checkpointID)
		if err != nil {
			if errors.Is(err, runtime.ErrRunnerRecordNotFound) {
				continue
			}
			return runtime.RestoredCheckpoint{}, err
		}
		snapshot, err := r.codec.Decode(payload)
		if err != nil {
			return runtime.RestoredCheckpoint{}, err
		}
		restored, err := state.RestoreStateSnapshot(snapshot)
		if err != nil {
			return runtime.RestoredCheckpoint{}, err
		}
		return runtime.RestoredCheckpoint{
			Record:    record,
			Snapshot:  restored.Snapshot,
			Business:  restored.Business,
			Runtime:   restored.Runtime,
			Artifacts: restored.Artifacts,
		}, nil
	}
	return runtime.RestoredCheckpoint{}, runtime.ErrRunnerRecordNotFound
}

func (r *graphCacheReader) ListEvents(runID string) ([]runtime.Event, error) {
	index, _, err := r.locateRun(context.Background(), runID)
	if err != nil {
		return nil, err
	}
	if index >= len(r.eventSinks) {
		return nil, runtime.ErrRunnerRecordNotFound
	}
	return r.eventSinks[index].ListEvents(runID)
}

func (r *graphCacheReader) ListEventPage(runID, cursor string, limit int) (runtime.EventPage, error) {
	index, _, err := r.locateRun(context.Background(), runID)
	if err != nil {
		return runtime.EventPage{}, err
	}
	if index >= len(r.eventSinks) {
		return runtime.EventPage{}, runtime.ErrRunnerRecordNotFound
	}
	return r.eventSinks[index].ListEventPage(runID, cursor, limit)
}

func (r *graphCacheReader) ListArtifacts(ctx context.Context, runID string) ([]state.ArtifactRef, error) {
	index, _, err := r.locateRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if index >= len(r.artifactStores) {
		return nil, runtime.ErrRunnerRecordNotFound
	}
	return r.artifactStores[index].List(ctx, runID)
}

func (r *graphCacheReader) LoadArtifact(ctx context.Context, ref state.ArtifactRef) (runtime.Artifact, error) {
	index, _, err := r.locateRun(ctx, ref.RunID)
	if err != nil {
		return runtime.Artifact{}, err
	}
	if index >= len(r.artifactStores) {
		return runtime.Artifact{}, runtime.ErrRunnerRecordNotFound
	}
	return r.artifactStores[index].Load(ctx, ref)
}
