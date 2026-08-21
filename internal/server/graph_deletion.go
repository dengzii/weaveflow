package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

type graphDeletionResponse struct {
	GraphID             string `json:"graph_id"`
	DeletedTriggerCount int    `json:"deleted_trigger_count"`
}

var errGraphHasActiveRuns = errors.New("graph has active runs")

func (s *Server) handleDeleteGraph(c *gin.Context) {
	graphID, ok := requireGraphIDPathParam(c)
	if !ok {
		return
	}
	if s == nil || s.runtime == nil {
		writeError(c, http.StatusServiceUnavailable, errRunnerNotConfigured)
		return
	}

	s.runtime.graphUpdateMu.Lock()
	defer s.runtime.graphUpdateMu.Unlock()
	if _, err := s.latestGraphSession(graphID); err != nil {
		if os.IsNotExist(err) {
			writeError(c, http.StatusNotFound, errTriggerGraphNotFound)
			return
		}
		writeError(c, statusForError(err), err)
		return
	}
	if err := s.runtime.deleteGraphRuntime(graphID); err != nil {
		if errors.Is(err, errGraphHasActiveRuns) {
			writeError(c, http.StatusConflict, err)
			return
		}
		writeError(c, statusForError(err), err)
		return
	}

	deletedTriggers, err := s.triggers.DeleteGraph(c.Request.Context(), graphID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	if err := os.RemoveAll(graphStorageDirectory(s.baseDir, graphID)); err != nil {
		writeError(c, http.StatusInternalServerError, fmt.Errorf("delete graph %q storage: %w", graphID, err))
		return
	}
	if s.events != nil {
		s.events.DeleteGraph(graphID)
	}
	if err := s.sweepManagedSecrets(c.Request.Context()); err != nil {
		slog.Warn("managed secret cleanup failed after graph deletion")
	}
	writeData(c, http.StatusOK, graphDeletionResponse{GraphID: graphID, DeletedTriggerCount: len(deletedTriggers)})
}

func (manager *graphRuntimeManager) deleteGraphRuntime(graphID string) error {
	if manager == nil {
		return nil
	}
	graphID = strings.TrimSpace(graphID)
	manager.mu.Lock()

	runners := make(map[runnerCloser]struct{})
	rememberRunner := func(session graphRuntimeSession) {
		if session.runner != nil && effectiveRunnerGraphID(session.runner) == graphID {
			runners[session.runner] = struct{}{}
		}
	}
	rememberRunner(manager.current)
	for key, session := range manager.sessions {
		if key.graphID == graphID {
			rememberRunner(session)
		}
	}
	for _, session := range manager.triggerSessions {
		rememberRunner(session)
	}
	for runner := range runners {
		if active := runner.ActiveRunCount(); active > 0 {
			manager.mu.Unlock()
			return fmt.Errorf("%w: graph %q has %d active runs", errGraphHasActiveRuns, graphID, active)
		}
	}

	if manager.current.runner != nil && effectiveRunnerGraphID(manager.current.runner) == graphID {
		manager.current = graphRuntimeSession{
			baseContext: manager.defaultContext,
			settings:    manager.defaultSettings,
		}
	}
	delete(manager.triggerSessions, graphID)
	for key := range manager.sessions {
		if key.graphID == graphID {
			delete(manager.sessions, key)
		}
	}
	store := manager.stores[graphID]
	delete(manager.stores, graphID)
	manager.mu.Unlock()

	var result error
	for runner := range runners {
		result = errors.Join(result, runner.Close())
	}
	if store.store != nil {
		result = errors.Join(result, store.store.Close())
	}
	return result
}

type runnerCloser interface {
	ActiveRunCount() int
	Close() error
}
