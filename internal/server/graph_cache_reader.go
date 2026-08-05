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
	sort.Slice(all, func(i, j int) bool {
		return all[i].StartedAt.Before(all[j].StartedAt)
	})
	return all, nil
}

func (r *combinedRunReader) GetRun(ctx context.Context, runID string) (runtime.RunRecord, error) {
	for _, reader := range r.readers {
		run, err := reader.GetRun(ctx, runID)
		if err == nil {
			return run, nil
		}
		if !errors.Is(err, runtime.ErrRunnerRecordNotFound) {
			return runtime.RunRecord{}, err
		}
	}
	return runtime.RunRecord{}, runtime.ErrRunnerRecordNotFound
}

func (r *combinedRunReader) ListSteps(ctx context.Context, runID string) ([]runtime.StepRecord, error) {
	for _, reader := range r.readers {
		steps, err := reader.ListSteps(ctx, runID)
		if err != nil {
			return nil, err
		}
		if len(steps) > 0 {
			return steps, nil
		}
	}
	return []runtime.StepRecord{}, nil
}

func (r *combinedRunReader) ListCheckpoints(ctx context.Context, runID string) ([]runtime.CheckpointRecord, error) {
	for _, reader := range r.readers {
		checkpoints, err := reader.ListCheckpoints(ctx, runID)
		if err != nil {
			return nil, err
		}
		if len(checkpoints) > 0 {
			return checkpoints, nil
		}
	}
	return []runtime.CheckpointRecord{}, nil
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
	for _, reader := range r.readers {
		events, err := reader.ListEvents(runID)
		if err != nil {
			return nil, err
		}
		if len(events) > 0 {
			return events, nil
		}
	}
	return []runtime.Event{}, nil
}

func (r *combinedRunReader) ListEventPage(runID, cursor string, limit int) (runtime.EventPage, error) {
	for _, reader := range r.readers {
		page, err := reader.ListEventPage(runID, cursor, limit)
		if err != nil {
			return runtime.EventPage{}, err
		}
		if len(page.Items) > 0 || page.NextCursor != "" {
			return page, nil
		}
	}
	return runtime.EventPage{Items: []runtime.Event{}}, nil
}

func (r *combinedRunReader) ListArtifacts(ctx context.Context, runID string) ([]state.ArtifactRef, error) {
	for _, reader := range r.readers {
		artifacts, err := reader.ListArtifacts(ctx, runID)
		if err != nil {
			return nil, err
		}
		if len(artifacts) > 0 {
			return artifacts, nil
		}
	}
	return []state.ArtifactRef{}, nil
}

func (r *combinedRunReader) LoadArtifact(ctx context.Context, ref state.ArtifactRef) (runtime.Artifact, error) {
	for _, reader := range r.readers {
		artifact, err := reader.LoadArtifact(ctx, ref)
		if err == nil {
			return artifact, nil
		}
		if !errors.Is(err, runtime.ErrRunnerRecordNotFound) {
			return runtime.Artifact{}, err
		}
	}
	return runtime.Artifact{}, runtime.ErrRunnerRecordNotFound
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
	sort.Slice(all, func(i, j int) bool {
		return all[i].StartedAt.Before(all[j].StartedAt)
	})
	return all, nil
}

func (r *graphCacheReader) GetRun(ctx context.Context, runID string) (runtime.RunRecord, error) {
	for _, store := range r.executionStores {
		run, err := store.GetRun(ctx, runID)
		if err == nil {
			return run, nil
		}
		if !errors.Is(err, runtime.ErrRunnerRecordNotFound) {
			return runtime.RunRecord{}, err
		}
	}
	return runtime.RunRecord{}, runtime.ErrRunnerRecordNotFound
}

func (r *graphCacheReader) ListSteps(ctx context.Context, runID string) ([]runtime.StepRecord, error) {
	for _, store := range r.executionStores {
		steps, err := store.ListSteps(ctx, runID)
		if err != nil {
			return nil, err
		}
		if len(steps) > 0 {
			return steps, nil
		}
	}
	return []runtime.StepRecord{}, nil
}

func (r *graphCacheReader) ListCheckpoints(ctx context.Context, runID string) ([]runtime.CheckpointRecord, error) {
	for _, store := range r.checkpointStores {
		checkpoints, err := store.List(ctx, runID)
		if err != nil {
			return nil, err
		}
		if len(checkpoints) > 0 {
			return checkpoints, nil
		}
	}
	return []runtime.CheckpointRecord{}, nil
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
	for _, sink := range r.eventSinks {
		events, err := sink.ListEvents(runID)
		if err != nil {
			return nil, err
		}
		if len(events) > 0 {
			return events, nil
		}
	}
	return []runtime.Event{}, nil
}

func (r *graphCacheReader) ListEventPage(runID, cursor string, limit int) (runtime.EventPage, error) {
	for _, sink := range r.eventSinks {
		page, err := sink.ListEventPage(runID, cursor, limit)
		if err != nil {
			return runtime.EventPage{}, err
		}
		if len(page.Items) > 0 || page.NextCursor != "" {
			return page, nil
		}
	}
	return runtime.EventPage{Items: []runtime.Event{}}, nil
}

func (r *graphCacheReader) ListArtifacts(ctx context.Context, runID string) ([]state.ArtifactRef, error) {
	for _, store := range r.artifactStores {
		artifacts, err := store.List(ctx, runID)
		if err != nil {
			return nil, err
		}
		if len(artifacts) > 0 {
			return artifacts, nil
		}
	}
	return []state.ArtifactRef{}, nil
}

func (r *graphCacheReader) LoadArtifact(ctx context.Context, ref state.ArtifactRef) (runtime.Artifact, error) {
	for _, store := range r.artifactStores {
		artifact, err := store.Load(ctx, ref)
		if err == nil {
			return artifact, nil
		}
		if !errors.Is(err, runtime.ErrRunnerRecordNotFound) {
			return runtime.Artifact{}, err
		}
	}
	return runtime.Artifact{}, runtime.ErrRunnerRecordNotFound
}
