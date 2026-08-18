package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	filestore "github.com/dengzii/weaveflow/internal/runtimestore/file"
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
	ListEventPage(runID, cursor string, limit int) (runtime.EventPage, error)
	ListArtifacts(ctx context.Context, runID string) ([]state.ArtifactRef, error)
	LoadArtifact(ctx context.Context, ref state.ArtifactRef) (runtime.Artifact, error)
}

type cachedGraphSummary struct {
	ID             string    `json:"id"`
	Name           string    `json:"name,omitempty"`
	GraphVersion   string    `json:"graph_version"`
	NodeCount      int       `json:"node_count"`
	SessionCount   int       `json:"session_count"`
	LatestSession  string    `json:"latest_session"`
	ActiveRunCount int       `json:"active_run_count"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type graphListPage struct {
	Items      []cachedGraphSummary `json:"items"`
	NextCursor string               `json:"next_cursor"`
}

type graphActiveState struct {
	ActiveRunCount int      `json:"active_run_count"`
	SessionIDs     []string `json:"session_ids,omitempty"`
}

type graphSessionSummary struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

type graphDetailResponse struct {
	Graph                    graphInfo                     `json:"graph"`
	Definition               dsl.GraphDefinition           `json:"definition"`
	Settings                 graphRuntimeSettings          `json:"settings"`
	InitialStateRequirements core.InitialStateRequirements `json:"initial_state_requirements"`
	LatestSession            graphSessionSummary           `json:"latest_session"`
	Active                   graphActiveState              `json:"active"`
}

func (s *Server) handleGetRetentionAudit(c *gin.Context) {
	graphID, ok := requireGraphIDPathParam(c)
	if !ok {
		return
	}
	audit := filestore.NewRetentionAuditSink(filepath.Join(s.graphHistoryBaseDir(graphID), "retention-audit.jsonl"))
	records, err := audit.List()
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, records)
}

type graphCacheReader struct {
	storeDirs        []string
	executionStores  []runtime.ExecutionReader
	checkpointStores []runtime.CheckpointReader
	artifactStores   []runtime.ArtifactReader
	eventReaders     []runtime.EventReader
	eventPageReaders []runtime.EventPageReader
	codec            state.Codec
}

type combinedRunReader struct {
	readers []runReader
}

const runExecutionRegistrationGracePeriod = time.Second

func (s *Server) resolveRunReader(c *gin.Context) runReader {
	graphID, ok := requireGraphIDPathParam(c)
	if !ok {
		return nil
	}
	runner := s.currentRunner()
	if graphID != "" {
		cache, err := s.openGraphCache(graphID)
		if err != nil {
			writeError(c, statusForError(err), err)
			return nil
		}
		if err := s.reconcileCachedRuns(c.Request.Context(), cache, runExecutionRegistrationGracePeriod); err != nil {
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
	limit, err := positiveIntQuery(c, "limit", 50, 200)
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	cursor, err := pageCursorQuery(c)
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	graphs, err := s.listCachedGraphs()
	if err != nil {
		writeError(c, http.StatusInternalServerError, err)
		return
	}
	if cursor > len(graphs) {
		writeError(c, http.StatusBadRequest, invalidRequestf("cursor is outside the graph collection"))
		return
	}
	end := min(cursor+limit, len(graphs))
	nextCursor := ""
	if end < len(graphs) {
		nextCursor = strconv.Itoa(end)
	}
	writeData(c, http.StatusOK, graphListPage{Items: graphs[cursor:end], NextCursor: nextCursor})
}

func (s *Server) handleGetGraphDetail(c *gin.Context) {
	graphID, ok := requireGraphIDPathParam(c)
	if !ok {
		return
	}
	stored, err := s.latestGraphSession(graphID)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(c, http.StatusNotFound, errTriggerGraphNotFound)
			return
		}
		writeError(c, statusForError(err), err)
		return
	}
	session, err := s.loadStoredGraphSession(stored, true)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	definition, err := session.graph.Definition()
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, graphDetailResponse{
		Graph: graphInfo{
			ID:                graphID,
			Version:           stored.manifest.GraphVersion,
			GraphHash:         stored.manifest.GraphHash,
			GraphSnapshotHash: stored.manifest.GraphSnapshotHash,
			GraphSessionID:    stored.manifest.GraphSessionID,
			EntryPoint:        definition.EntryPoint,
			FinishPoint:       definition.FinishPoint,
		},
		Definition:               definition,
		Settings:                 s.graphSettingsResponse(session.settings),
		InitialStateRequirements: session.graph.InitialStateRequirements(),
		LatestSession: graphSessionSummary{
			ID:        stored.manifest.GraphSessionID,
			CreatedAt: stored.manifest.CreatedAt,
		},
		Active: s.runtime.graphActiveState(graphID),
	})
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
	latestCreatedAt := make(map[string]time.Time)
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
			latestAt := latestCreatedAt[manifest.GraphID]
			if manifest.CreatedAt.After(latestAt) ||
				(manifest.CreatedAt.Equal(latestAt) && manifest.GraphSessionID > summary.LatestSession) {
				summary.Name = manifest.GraphName
				summary.GraphVersion = manifest.GraphVersion
				summary.NodeCount = manifest.NodeCount
				summary.LatestSession = manifest.GraphSessionID
				summary.UpdatedAt = manifest.CreatedAt
				summary.ActiveRunCount = s.runtime.graphActiveState(manifest.GraphID).ActiveRunCount
				latestCreatedAt[manifest.GraphID] = manifest.CreatedAt
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

func pageCursorQuery(c *gin.Context) (int, error) {
	value, err := optionalStringQuery(c, "cursor")
	if err != nil || value == "" {
		return 0, err
	}
	cursor, err := strconv.Atoi(value)
	if err != nil || cursor < 0 {
		return 0, invalidRequestf("cursor is invalid")
	}
	return cursor, nil
}

func (s *Server) openGraphCache(graphID string) (*graphCacheReader, error) {
	reader := &graphCacheReader{
		codec: state.NewJSONStateCodec(""),
	}
	graphDir := graphStorageDirectory(s.baseDir, graphID)
	historyDir := filepath.Join(graphDir, "history")
	if err := reader.appendStore(historyDir); err != nil {
		return nil, err
	}
	sessions, err := os.ReadDir(graphDir)
	if os.IsNotExist(err) {
		return reader, nil
	}
	if err != nil {
		return nil, err
	}
	for _, sess := range sessions {
		if !sess.IsDir() || sess.Name() == "history" {
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
		if err := reader.appendStore(base); err != nil {
			return nil, err
		}
	}
	return reader, nil
}

func (reader *graphCacheReader) appendStore(baseDir string) error {
	storeReader, err := filestore.OpenReader(baseDir)
	if err != nil {
		return err
	}
	reader.storeDirs = append(reader.storeDirs, baseDir)
	reader.executionStores = append(reader.executionStores, storeReader.ExecutionReader())
	reader.checkpointStores = append(reader.checkpointStores, storeReader.CheckpointReader())
	reader.artifactStores = append(reader.artifactStores, storeReader.ArtifactReader())
	reader.eventReaders = append(reader.eventReaders, storeReader.EventReader())
	reader.eventPageReaders = append(reader.eventPageReaders, storeReader.EventPageReader())
	return nil
}

func (s *Server) reconcileCachedRuns(ctx context.Context, reader *graphCacheReader, gracePeriod time.Duration) error {
	if reader == nil {
		return nil
	}
	cutoff := time.Now().Add(-gracePeriod)
	for index, store := range reader.executionStores {
		runs, err := store.ListRuns(ctx, runtime.RunFilter{Statuses: []runtime.RunStatus{
			runtime.RunStatusPending,
			runtime.RunStatusRunning,
		}})
		if err != nil {
			return err
		}
		for _, run := range runs {
			if runtime.IsExecutionLeaseActive(run, time.Now()) {
				continue
			}
			if !run.StartedAt.IsZero() && run.StartedAt.After(cutoff) {
				continue
			}
			session := s.runtime.session(run.GraphID, run.GraphSessionID)
			if session.runner != nil {
				if session.runner.IsRunActive(run.RunID) {
					continue
				}
				control, err := runtime.NewRunControlService(
					session.runner.ExecutionStore(),
					session.runner.TransactionStore(),
					session.runner.EventSink(),
					nil,
				)
				if err != nil {
					return err
				}
				if _, err := control.MarkRunExecutionLost(ctx, run.RunID); err != nil {
					return err
				}
				continue
			}
			if _, err := s.markCachedRunExecutionLost(ctx, reader, index, run.RunID); errors.Is(err, filestore.ErrWriterLocked) {
				continue
			} else if err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Server) markCachedRunExecutionLost(ctx context.Context, reader *graphCacheReader, index int, runID string) (runtime.RunRecord, error) {
	if reader == nil || index < 0 || index >= len(reader.executionStores) {
		return runtime.RunRecord{}, runtime.ErrRunnerRecordNotFound
	}
	if index >= len(reader.storeDirs) {
		return runtime.RunRecord{}, fmt.Errorf("runtime store directory %d is missing", index)
	}
	store, err := filestore.Open(reader.storeDirs[index])
	if err != nil {
		return runtime.RunRecord{}, err
	}
	defer func() { _ = store.Close() }()
	eventSink := runtime.EventSink(nil)
	if s.events != nil {
		eventSink = s.events
	}
	control, err := runtime.NewRunControlService(store.ExecutionStore(), store.TransactionStore(), eventSink, nil)
	if err != nil {
		return runtime.RunRecord{}, err
	}
	return control.MarkRunExecutionLost(ctx, runID)
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
	settingsPath := strings.TrimSpace(manifest.SettingsPath)
	if settingsPath == "" {
		return graphSessionManifest{}, false, fmt.Errorf("graph session %q settings path is missing", sessionID)
	}
	settingsName := filepath.Clean(settingsPath)
	if filepath.IsAbs(settingsName) || settingsName != filepath.Base(settingsName) {
		return graphSessionManifest{}, false, fmt.Errorf("graph session %q settings path is invalid", sessionID)
	}
	manifest.SettingsPath = settingsName
	manifest.RuntimeSettingsHash = strings.TrimSpace(manifest.RuntimeSettingsHash)
	if manifest.RuntimeSettingsHash == "" {
		return graphSessionManifest{}, false, fmt.Errorf("graph session %q runtime settings hash is missing", sessionID)
	}
	if _, err := os.Stat(filepath.Join(baseDir, definitionName)); os.IsNotExist(err) {
		return graphSessionManifest{}, false, nil
	} else if err != nil {
		return graphSessionManifest{}, false, err
	}
	settingsData, err := os.ReadFile(filepath.Join(baseDir, settingsName))
	if os.IsNotExist(err) {
		return graphSessionManifest{}, false, nil
	}
	if err != nil {
		return graphSessionManifest{}, false, err
	}
	settingsHash := graphRuntimeSettingsDataHash(settingsData)
	if settingsHash != manifest.RuntimeSettingsHash {
		return graphSessionManifest{}, false, fmt.Errorf("graph session %q runtime settings hash mismatch", sessionID)
	}
	if _, found, err := loadGraphRuntimeSettings(baseDir); err != nil {
		return graphSessionManifest{}, false, err
	} else if !found {
		return graphSessionManifest{}, false, nil
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
	if id := strings.TrimSpace(runner.GraphID()); id != "" {
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
		run, err := reader.cancelPausedRun(ctx, runID, s.events)
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

func (r *graphCacheReader) cancelPausedRun(ctx context.Context, runID string, extraSink runtime.EventSink) (runtime.RunRecord, error) {
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
			if index >= len(r.storeDirs) {
				return runtime.RunRecord{}, fmt.Errorf("runtime store directory %d is missing", index)
			}
			writer, err := filestore.Open(r.storeDirs[index])
			if errors.Is(err, filestore.ErrWriterLocked) {
				return runtime.RunRecord{}, fmt.Errorf("%w: run %q is owned by another writer", runtime.ErrRunControlNotAllowed, runID)
			}
			if err != nil {
				return runtime.RunRecord{}, err
			}
			defer func() { _ = writer.Close() }()
			control, err := runtime.NewRunControlService(writer.ExecutionStore(), writer.TransactionStore(), extraSink, nil)
			if err != nil {
				return runtime.RunRecord{}, err
			}
			return control.CancelPausedRun(ctx, runID)
		default:
			return runtime.RunRecord{}, fmt.Errorf("%w: run %q status %q cannot be canceled without an active runner", runtime.ErrRunControlNotAllowed, runID, run.Status)
		}
	}
	return runtime.RunRecord{}, runtime.ErrRunnerRecordNotFound
}

func (r *graphCacheReader) deleteRun(ctx context.Context, runID string) (runtime.RunRecord, error) {
	for index, store := range r.executionStores {
		if _, err := store.GetRun(ctx, runID); errors.Is(err, runtime.ErrRunnerRecordNotFound) {
			continue
		} else if err != nil {
			return runtime.RunRecord{}, err
		}
		if index >= len(r.storeDirs) {
			return runtime.RunRecord{}, fmt.Errorf("runtime store directory %d is missing", index)
		}
		writer, err := filestore.Open(r.storeDirs[index])
		if errors.Is(err, filestore.ErrWriterLocked) {
			return runtime.RunRecord{}, fmt.Errorf("%w: run %q is owned by another writer", runtime.ErrRunControlNotAllowed, runID)
		}
		if err != nil {
			return runtime.RunRecord{}, err
		}
		deleter := runtime.NewRunDeletionCoordinator(
			writer.ExecutionDeletionStore(),
			writer.CheckpointDeleter(),
			writer.EventDeleter(),
			writer.ArtifactDeleter(),
		)
		control, err := runtime.NewRunControlService(writer.ExecutionStore(), writer.TransactionStore(), writer.EventSink(), deleter)
		if err != nil {
			_ = writer.Close()
			return runtime.RunRecord{}, err
		}
		run, err := control.DeleteRun(ctx, runID)
		closeErr := writer.Close()
		if err == nil {
			err = closeErr
		}
		if errors.Is(err, runtime.ErrRunnerRecordNotFound) {
			continue
		}
		return run, err
	}
	return runtime.RunRecord{}, runtime.ErrRunnerRecordNotFound
}
