package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"

	"github.com/gin-gonic/gin"
)

type runReader interface {
	ListRuns(ctx context.Context, filter runtime.RunFilter) ([]runtime.RunRecord, error)
	GetRun(ctx context.Context, runID string) (runtime.RunRecord, error)
	ListSteps(ctx context.Context, runID string) ([]runtime.StepRecord, error)
	ListCheckpoints(ctx context.Context, runID string) ([]runtime.CheckpointRecord, error)
	LoadCheckpointState(ctx context.Context, checkpointID string) (runtime.RestoredCheckpoint, error)
	ListEvents(runID string) ([]runtime.Event, error)
	ListArtifacts(ctx context.Context, runID string) ([]state.ArtifactRef, error)
	LoadArtifact(ctx context.Context, ref state.ArtifactRef) (runtime.Artifact, error)
}

type cachedGraphSummary struct {
	ID            string `json:"id"`
	SessionCount  int    `json:"session_count"`
	LatestSession string `json:"latest_session,omitempty"`
}

type graphCacheReader struct {
	executionStores  []*runtime.FileExecutionStore
	checkpointStores []*runtime.FileCheckpointStore
	artifactStores   []*runtime.FileArtifactStore
	eventSinks       []*runtime.FileEventSink
	codec            state.StateCodec
}

type combinedRunReader struct {
	readers []runReader
}

func (s *Server) resolveRunReader(c *gin.Context) runReader {
	graphID := strings.TrimSpace(c.Query("graph_id"))
	runner := s.currentRunner()
	if graphID != "" {
		cache := s.openGraphCache(graphID)
		if runner != nil && graphID == effectiveRunnerGraphID(runner) {
			if cache.hasSessions() {
				return combineRunReaders(runner, cache)
			}
			return runner
		}
		return cache
	}
	if runner != nil {
		return runner
	}
	writeError(c, http.StatusServiceUnavailable, errRunnerNotConfigured)
	return nil
}

func (s *Server) handleListGraphs(c *gin.Context) {
	graphs, err := s.listCachedGraphs()
	if err != nil {
		writeError(c, http.StatusInternalServerError, err)
		return
	}
	writeData(c, http.StatusOK, graphs)
}

func (s *Server) listCachedGraphs() ([]cachedGraphSummary, error) {
	graphsDir := filepath.Join(s.baseDir, "graphs")
	entries, err := os.ReadDir(graphsDir)
	if os.IsNotExist(err) {
		return []cachedGraphSummary{}, nil
	}
	if err != nil {
		return nil, err
	}
	var result []cachedGraphSummary
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		graphDir := filepath.Join(graphsDir, entry.Name())
		sessions, _ := os.ReadDir(graphDir)
		var sessionNames []string
		for _, sess := range sessions {
			if sess.IsDir() {
				sessionNames = append(sessionNames, sess.Name())
			}
		}
		sort.Strings(sessionNames)
		var latest string
		if len(sessionNames) > 0 {
			latest = sessionNames[len(sessionNames)-1]
		}
		result = append(result, cachedGraphSummary{
			ID:            entry.Name(),
			SessionCount:  len(sessionNames),
			LatestSession: latest,
		})
	}
	return result, nil
}

func (s *Server) openGraphCache(graphID string) *graphCacheReader {
	safe := safePathSegment(graphID)
	graphDir := filepath.Join(s.baseDir, "graphs", safe)
	sessions, err := os.ReadDir(graphDir)
	if err != nil {
		return &graphCacheReader{codec: state.NewJSONStateCodec("")}
	}

	reader := &graphCacheReader{
		codec: state.NewJSONStateCodec(""),
	}
	for _, sess := range sessions {
		if !sess.IsDir() {
			continue
		}
		base := filepath.Join(graphDir, sess.Name())
		reader.executionStores = append(reader.executionStores, runtime.NewFileExecutionStore(filepath.Join(base, "execution")))
		reader.checkpointStores = append(reader.checkpointStores, runtime.NewFileCheckpointStore(filepath.Join(base, "checkpoints")))
		reader.artifactStores = append(reader.artifactStores, runtime.NewFileArtifactStore(filepath.Join(base, "artifacts")))
		reader.eventSinks = append(reader.eventSinks, runtime.NewFileEventSink(filepath.Join(base, "events")))
	}
	return reader
}

func (r *graphCacheReader) hasSessions() bool {
	return r != nil && len(r.executionStores) > 0
}

func effectiveRunnerGraphID(runner *runtime.GraphRunner) string {
	if runner == nil {
		return ""
	}
	if id := strings.TrimSpace(runner.GraphID); id != "" {
		return id
	}
	return "graph"
}

func combineRunReaders(readers ...runReader) runReader {
	combined := &combinedRunReader{}
	for _, reader := range readers {
		if reader != nil {
			combined.readers = append(combined.readers, reader)
		}
	}
	return combined
}

func (s *Server) cancelCachedPausedRun(ctx context.Context, graphID string, runID string) (runtime.RunRecord, error) {
	readers, err := s.cachedGraphReaders(graphID)
	if err != nil {
		return runtime.RunRecord{}, err
	}
	for _, reader := range readers {
		run, err := reader.cancelPausedRun(ctx, runID)
		if err == nil {
			return run, nil
		}
		if errors.Is(err, runtime.ErrRunnerRecordNotFound) {
			continue
		}
		return runtime.RunRecord{}, err
	}
	return runtime.RunRecord{}, runtime.ErrRunnerRecordNotFound
}

func (s *Server) deleteCachedRun(ctx context.Context, graphID string, runID string) (runtime.RunRecord, error) {
	readers, err := s.cachedGraphReaders(graphID)
	if err != nil {
		return runtime.RunRecord{}, err
	}
	for _, reader := range readers {
		run, err := reader.deleteRun(ctx, runID)
		if err == nil {
			return run, nil
		}
		if errors.Is(err, runtime.ErrRunnerRecordNotFound) {
			continue
		}
		return runtime.RunRecord{}, err
	}
	return runtime.RunRecord{}, runtime.ErrRunnerRecordNotFound
}

func (s *Server) cachedGraphReaders(graphID string) ([]*graphCacheReader, error) {
	if strings.TrimSpace(graphID) != "" {
		return []*graphCacheReader{s.openGraphCache(graphID)}, nil
	}
	graphs, err := s.listCachedGraphs()
	if err != nil {
		return nil, err
	}
	readers := make([]*graphCacheReader, 0, len(graphs))
	for _, graph := range graphs {
		readers = append(readers, s.openGraphCache(graph.ID))
	}
	return readers, nil
}

func (r *graphCacheReader) cancelPausedRun(ctx context.Context, runID string) (runtime.RunRecord, error) {
	for index, store := range r.executionStores {
		run, err := store.GetRun(ctx, runID)
		if err != nil {
			if errors.Is(err, runtime.ErrRunnerRecordNotFound) {
				continue
			}
			return runtime.RunRecord{}, err
		}
		switch run.Status {
		case runtime.RunStatusCanceled:
			return run, nil
		case runtime.RunStatusPaused:
			return r.cancelPausedRunInStore(ctx, index, store, run)
		default:
			return runtime.RunRecord{}, fmt.Errorf("%w: run %q status %q cannot be canceled without an active runner", runtime.ErrRunControlNotAllowed, runID, run.Status)
		}
	}
	return runtime.RunRecord{}, runtime.ErrRunnerRecordNotFound
}

func (r *graphCacheReader) deleteRun(ctx context.Context, runID string) (runtime.RunRecord, error) {
	for index, store := range r.executionStores {
		run, err := store.GetRun(ctx, runID)
		if err != nil {
			if errors.Is(err, runtime.ErrRunnerRecordNotFound) {
				continue
			}
			return runtime.RunRecord{}, err
		}
		if err := r.deleteRunInStore(ctx, index, store, runID); err != nil {
			return runtime.RunRecord{}, err
		}
		return run, nil
	}
	return runtime.RunRecord{}, runtime.ErrRunnerRecordNotFound
}

func (r *graphCacheReader) deleteRunInStore(ctx context.Context, index int, store *runtime.FileExecutionStore, runID string) error {
	if index >= 0 && index < len(r.checkpointStores) && r.checkpointStores[index] != nil {
		if err := r.checkpointStores[index].DeleteRun(ctx, runID); err != nil {
			return err
		}
	}
	if index >= 0 && index < len(r.artifactStores) && r.artifactStores[index] != nil {
		if err := r.artifactStores[index].DeleteRun(ctx, runID); err != nil {
			return err
		}
	}
	if index >= 0 && index < len(r.eventSinks) && r.eventSinks[index] != nil {
		if err := r.eventSinks[index].DeleteRun(ctx, runID); err != nil {
			return err
		}
	}
	return store.DeleteRun(ctx, runID)
}

func (r *graphCacheReader) cancelPausedRunInStore(ctx context.Context, index int, store *runtime.FileExecutionStore, run runtime.RunRecord) (runtime.RunRecord, error) {
	now := time.Now().UTC()

	requested := run
	requested.PauseRequested = false
	requested.CancelRequested = true
	requested.UpdatedAt = now
	if err := store.UpdateRun(ctx, requested); err != nil {
		return runtime.RunRecord{}, err
	}
	if err := r.publishCachedRunEvent(ctx, index, requested, runtime.EventRunCancelRequested, now); err != nil {
		return runtime.RunRecord{}, err
	}

	canceled := requested
	canceled.Status = runtime.RunStatusCanceled
	canceled.CancelRequested = false
	canceled.UpdatedAt = now
	canceled.FinishedAt = &now
	if err := store.UpdateRun(ctx, canceled); err != nil {
		return runtime.RunRecord{}, err
	}
	if err := r.publishCachedRunEvent(ctx, index, canceled, runtime.EventRunCanceled, now); err != nil {
		return runtime.RunRecord{}, err
	}
	return canceled, nil
}

func (r *graphCacheReader) publishCachedRunEvent(ctx context.Context, index int, run runtime.RunRecord, eventType runtime.EventType, at time.Time) error {
	if index < 0 || index >= len(r.eventSinks) || r.eventSinks[index] == nil {
		return nil
	}
	return r.eventSinks[index].Publish(ctx, runtime.Event{
		ID:        cachedRunEventID(eventType, run.RunID, at),
		RunID:     run.RunID,
		NodeID:    run.CurrentNodeID,
		Type:      eventType,
		Timestamp: at,
	})
}

func cachedRunEventID(eventType runtime.EventType, runID string, at time.Time) string {
	return fmt.Sprintf("server-%s-%s-%d", eventType, runID, at.UnixNano())
}

func (r *combinedRunReader) ListRuns(ctx context.Context, filter runtime.RunFilter) ([]runtime.RunRecord, error) {
	byID := make(map[string]runtime.RunRecord)
	var lastErr error
	sawSuccess := false
	for _, reader := range r.readers {
		runs, err := reader.ListRuns(ctx, filter)
		if err != nil {
			lastErr = err
			continue
		}
		sawSuccess = true
		for _, run := range runs {
			if _, exists := byID[run.RunID]; !exists {
				byID[run.RunID] = run
			}
		}
	}
	if len(byID) == 0 && lastErr != nil && !sawSuccess {
		return nil, lastErr
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
	var lastErr error
	for _, reader := range r.readers {
		run, err := reader.GetRun(ctx, runID)
		if err == nil {
			return run, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return runtime.RunRecord{}, lastErr
	}
	return runtime.RunRecord{}, runtime.ErrRunnerRecordNotFound
}

func (r *combinedRunReader) ListSteps(ctx context.Context, runID string) ([]runtime.StepRecord, error) {
	var lastErr error
	sawSuccess := false
	for _, reader := range r.readers {
		steps, err := reader.ListSteps(ctx, runID)
		if err != nil {
			lastErr = err
			continue
		}
		sawSuccess = true
		if len(steps) > 0 {
			return steps, nil
		}
	}
	if lastErr != nil && !sawSuccess {
		return nil, lastErr
	}
	return []runtime.StepRecord{}, nil
}

func (r *combinedRunReader) ListCheckpoints(ctx context.Context, runID string) ([]runtime.CheckpointRecord, error) {
	var lastErr error
	sawSuccess := false
	for _, reader := range r.readers {
		checkpoints, err := reader.ListCheckpoints(ctx, runID)
		if err != nil {
			lastErr = err
			continue
		}
		sawSuccess = true
		if len(checkpoints) > 0 {
			return checkpoints, nil
		}
	}
	if lastErr != nil && !sawSuccess {
		return nil, lastErr
	}
	return []runtime.CheckpointRecord{}, nil
}

func (r *combinedRunReader) LoadCheckpointState(ctx context.Context, checkpointID string) (runtime.RestoredCheckpoint, error) {
	var lastErr error
	for _, reader := range r.readers {
		checkpoint, err := reader.LoadCheckpointState(ctx, checkpointID)
		if err == nil {
			return checkpoint, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return runtime.RestoredCheckpoint{}, lastErr
	}
	return runtime.RestoredCheckpoint{}, runtime.ErrRunnerRecordNotFound
}

func (r *combinedRunReader) ListEvents(runID string) ([]runtime.Event, error) {
	var lastErr error
	sawSuccess := false
	for _, reader := range r.readers {
		events, err := reader.ListEvents(runID)
		if err != nil {
			lastErr = err
			continue
		}
		sawSuccess = true
		if len(events) > 0 {
			return events, nil
		}
	}
	if lastErr != nil && !sawSuccess {
		return nil, lastErr
	}
	return []runtime.Event{}, nil
}

func (r *combinedRunReader) ListArtifacts(ctx context.Context, runID string) ([]state.ArtifactRef, error) {
	var lastErr error
	sawSuccess := false
	for _, reader := range r.readers {
		artifacts, err := reader.ListArtifacts(ctx, runID)
		if err != nil {
			lastErr = err
			continue
		}
		sawSuccess = true
		if len(artifacts) > 0 {
			return artifacts, nil
		}
	}
	if lastErr != nil && !sawSuccess {
		return nil, lastErr
	}
	return []state.ArtifactRef{}, nil
}

func (r *combinedRunReader) LoadArtifact(ctx context.Context, ref state.ArtifactRef) (runtime.Artifact, error) {
	var lastErr error
	for _, reader := range r.readers {
		artifact, err := reader.LoadArtifact(ctx, ref)
		if err == nil {
			return artifact, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return runtime.Artifact{}, lastErr
	}
	return runtime.Artifact{}, runtime.ErrRunnerRecordNotFound
}

func (r *graphCacheReader) ListRuns(ctx context.Context, filter runtime.RunFilter) ([]runtime.RunRecord, error) {
	all := make([]runtime.RunRecord, 0)
	for _, store := range r.executionStores {
		runs, err := store.ListRuns(ctx, filter)
		if err != nil {
			continue
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
	}
	return runtime.RunRecord{}, runtime.ErrRunnerRecordNotFound
}

func (r *graphCacheReader) ListSteps(ctx context.Context, runID string) ([]runtime.StepRecord, error) {
	for _, store := range r.executionStores {
		steps, err := store.ListSteps(ctx, runID)
		if err == nil && len(steps) > 0 {
			return steps, nil
		}
	}
	return []runtime.StepRecord{}, nil
}

func (r *graphCacheReader) ListCheckpoints(ctx context.Context, runID string) ([]runtime.CheckpointRecord, error) {
	for _, store := range r.checkpointStores {
		checkpoints, err := store.List(ctx, runID)
		if err == nil && len(checkpoints) > 0 {
			return checkpoints, nil
		}
	}
	return []runtime.CheckpointRecord{}, nil
}

func (r *graphCacheReader) LoadCheckpointState(ctx context.Context, checkpointID string) (runtime.RestoredCheckpoint, error) {
	for _, store := range r.checkpointStores {
		record, payload, err := store.Load(ctx, checkpointID)
		if err != nil {
			continue
		}
		snapshot, err := r.codec.Decode(payload)
		if err != nil {
			continue
		}
		restored, err := state.RestoreStateSnapshot(snapshot)
		if err != nil {
			continue
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
		if err == nil && len(events) > 0 {
			return events, nil
		}
	}
	return []runtime.Event{}, nil
}

func (r *graphCacheReader) ListArtifacts(ctx context.Context, runID string) ([]state.ArtifactRef, error) {
	for _, store := range r.artifactStores {
		artifacts, err := store.List(ctx, runID)
		if err == nil && len(artifacts) > 0 {
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
	}
	return runtime.Artifact{}, runtime.ErrRunnerRecordNotFound
}
