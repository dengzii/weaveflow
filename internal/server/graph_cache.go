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
	graphID, err := optionalStringQuery(c, "graph_id")
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return nil
	}
	runner := s.currentRunner()
	if graphID != "" {
		cache, err := s.openGraphCache(graphID)
		if err != nil {
			writeError(c, statusForError(err), err)
			return nil
		}
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
	byGraphID := make(map[string]*cachedGraphSummary)
	seenSessions := make(map[string]struct{})
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		graphDir := filepath.Join(graphsDir, entry.Name())
		sessions, err := os.ReadDir(graphDir)
		if err != nil {
			return nil, fmt.Errorf("read graph storage %q: %w", entry.Name(), err)
		}
		for _, sess := range sessions {
			if !sess.IsDir() {
				continue
			}
			manifest, complete, err := readCachedGraphSession(graphDir, sess.Name())
			if err != nil {
				return nil, err
			}
			if !complete {
				continue
			}
			key := manifest.GraphID + "\x00" + manifest.GraphSessionID
			if _, exists := seenSessions[key]; exists {
				continue
			}
			seenSessions[key] = struct{}{}
			summary := byGraphID[manifest.GraphID]
			if summary == nil {
				summary = &cachedGraphSummary{ID: manifest.GraphID}
				byGraphID[manifest.GraphID] = summary
			}
			summary.SessionCount++
			if manifest.GraphSessionID > summary.LatestSession {
				summary.LatestSession = manifest.GraphSessionID
			}
		}
	}
	result := make([]cachedGraphSummary, 0, len(byGraphID))
	for _, summary := range byGraphID {
		result = append(result, *summary)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (s *Server) openGraphCache(graphID string) (*graphCacheReader, error) {
	reader := &graphCacheReader{
		codec: state.NewJSONStateCodec(""),
	}
	graphDir := graphStorageDirectory(s.baseDir, graphID)
	sessions, err := os.ReadDir(graphDir)
	if os.IsNotExist(err) {
		return reader, nil
	}
	if err != nil {
		return nil, err
	}
	for _, sess := range sessions {
		if !sess.IsDir() {
			continue
		}
		manifest, complete, err := readCachedGraphSession(graphDir, sess.Name())
		if err != nil {
			return nil, err
		}
		if !complete || manifest.GraphID != graphID {
			continue
		}
		base := filepath.Join(graphDir, sess.Name())
		reader.executionStores = append(reader.executionStores, runtime.NewFileExecutionStore(filepath.Join(base, "execution")))
		reader.checkpointStores = append(reader.checkpointStores, runtime.NewFileCheckpointStore(filepath.Join(base, "checkpoints")))
		reader.artifactStores = append(reader.artifactStores, runtime.NewFileArtifactStore(filepath.Join(base, "artifacts")))
		reader.eventSinks = append(reader.eventSinks, runtime.NewFileEventSink(filepath.Join(base, "events")))
	}
	return reader, nil
}

func readCachedGraphSession(graphDir string, sessionID string) (graphSessionManifest, bool, error) {
	baseDir := filepath.Join(graphDir, sessionID)
	manifestData, err := os.ReadFile(filepath.Join(baseDir, "graph.json"))
	if os.IsNotExist(err) {
		return graphSessionManifest{}, false, nil
	}
	if err != nil {
		return graphSessionManifest{}, false, err
	}
	var manifest graphSessionManifest
	if err := decodeStrictJSON(manifestData, &manifest); err != nil {
		return graphSessionManifest{}, false, fmt.Errorf("decode graph session %q manifest: %w", sessionID, err)
	}
	manifest.GraphID = strings.TrimSpace(manifest.GraphID)
	if manifest.GraphID == "" {
		return graphSessionManifest{}, false, fmt.Errorf("graph session %q graph id is missing", sessionID)
	}
	manifest.GraphVersion = strings.TrimSpace(manifest.GraphVersion)
	if manifest.GraphVersion == "" {
		return graphSessionManifest{}, false, fmt.Errorf("graph session %q graph version is missing", sessionID)
	}
	manifest.GraphHash = strings.TrimSpace(manifest.GraphHash)
	if manifest.GraphHash == "" {
		return graphSessionManifest{}, false, fmt.Errorf("graph session %q graph hash is missing", sessionID)
	}
	manifest.GraphSnapshotHash = strings.TrimSpace(manifest.GraphSnapshotHash)
	if manifest.GraphSnapshotHash == "" {
		return graphSessionManifest{}, false, fmt.Errorf("graph session %q graph snapshot hash is missing", sessionID)
	}
	manifest.GraphSessionID = strings.TrimSpace(manifest.GraphSessionID)
	if manifest.GraphSessionID == "" {
		return graphSessionManifest{}, false, fmt.Errorf("graph session %q manifest id is missing", sessionID)
	}
	if manifest.GraphSessionID != sessionID {
		return graphSessionManifest{}, false, fmt.Errorf("graph session %q manifest id mismatch", sessionID)
	}
	definitionPath := strings.TrimSpace(manifest.DefinitionPath)
	if definitionPath == "" {
		return graphSessionManifest{}, false, fmt.Errorf("graph session %q definition path is missing", sessionID)
	}
	definitionName := filepath.Clean(definitionPath)
	if filepath.IsAbs(definitionName) || definitionName != filepath.Base(definitionName) {
		return graphSessionManifest{}, false, fmt.Errorf("graph session %q definition path is invalid", sessionID)
	}
	if manifest.CreatedAt.IsZero() {
		return graphSessionManifest{}, false, fmt.Errorf("graph session %q created_at is missing", sessionID)
	}
	manifest.DefinitionPath = definitionName
	if _, err := os.Stat(filepath.Join(baseDir, definitionName)); os.IsNotExist(err) {
		return graphSessionManifest{}, false, nil
	} else if err != nil {
		return graphSessionManifest{}, false, err
	}
	return manifest, true, nil
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
		reader, err := s.openGraphCache(graphID)
		if err != nil {
			return nil, err
		}
		return []*graphCacheReader{reader}, nil
	}
	graphs, err := s.listCachedGraphs()
	if err != nil {
		return nil, err
	}
	readers := make([]*graphCacheReader, 0, len(graphs))
	for _, graph := range graphs {
		reader, err := s.openGraphCache(graph.ID)
		if err != nil {
			return nil, err
		}
		readers = append(readers, reader)
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
		var checkpointStore runtime.RunDeleter
		if index < len(r.checkpointStores) {
			checkpointStore = r.checkpointStores[index]
		}
		var artifactStore runtime.RunDeleter
		if index < len(r.artifactStores) {
			artifactStore = r.artifactStores[index]
		}
		var eventStore runtime.RunDeleter
		if index < len(r.eventSinks) {
			eventStore = r.eventSinks[index]
		}
		deleter := runtime.NewRunDeletionCoordinator(store, checkpointStore, eventStore, artifactStore)
		if err := deleter.DeleteRun(ctx, runID); err != nil {
			return runtime.RunRecord{}, err
		}
		return run, nil
	}
	return runtime.RunRecord{}, runtime.ErrRunnerRecordNotFound
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
