package server

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"

	wfgraph "github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/runtime"
)

type graphRuntimeSession struct {
	graph       *wfgraph.Graph
	runner      *runtime.GraphRunner
	baseContext context.Context
	settings    graphRuntimeSettings
}

type graphRuntimeSessionKey struct {
	graphID   string
	sessionID string
}

type graphRuntimeManager struct {
	mu              sync.RWMutex
	graphUpdateMu   sync.Mutex
	current         graphRuntimeSession
	defaultContext  context.Context
	defaultSettings graphRuntimeSettings
	triggerSessions map[string]graphRuntimeSession
	sessions        map[graphRuntimeSessionKey]graphRuntimeSession
}

func newGraphRuntimeManager(
	baseContext context.Context,
	settings graphRuntimeSettings,
	graph *wfgraph.Graph,
	runner *runtime.GraphRunner,
) *graphRuntimeManager {
	if baseContext == nil {
		baseContext = context.Background()
	}
	manager := &graphRuntimeManager{
		current: graphRuntimeSession{
			graph:       graph,
			runner:      runner,
			baseContext: baseContext,
			settings:    normalizedGraphSettings(settings),
		},
		defaultContext:  baseContext,
		defaultSettings: normalizedGraphSettings(settings),
		triggerSessions: make(map[string]graphRuntimeSession),
		sessions:        make(map[graphRuntimeSessionKey]graphRuntimeSession),
	}
	if runner != nil {
		manager.triggerSessions[effectiveRunnerGraphID(runner)] = manager.current
		manager.rememberSessionLocked(manager.current)
	}
	return manager
}

func (manager *graphRuntimeManager) currentSession() graphRuntimeSession {
	if manager == nil {
		return graphRuntimeSession{}
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current
}

func (manager *graphRuntimeManager) installSession(session graphRuntimeSession) {
	if manager == nil {
		return
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.current = session
	if session.runner != nil {
		manager.triggerSessions[effectiveRunnerGraphID(session.runner)] = session
		manager.rememberSessionLocked(session)
	}
}

func (manager *graphRuntimeManager) runtimeContext() context.Context {
	if manager == nil {
		return context.Background()
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	if manager.current.baseContext == nil {
		return context.Background()
	}
	return manager.current.baseContext
}

func (manager *graphRuntimeManager) defaults() (graphRuntimeSettings, context.Context) {
	if manager == nil {
		return graphRuntimeSettingsFromContext(context.Background()), context.Background()
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return normalizedGraphSettings(manager.defaultSettings), manager.defaultContext
}

func (manager *graphRuntimeManager) triggerSession(graphID string) graphRuntimeSession {
	if manager == nil {
		return graphRuntimeSession{}
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.triggerSessions[strings.TrimSpace(graphID)]
}

func (manager *graphRuntimeManager) cacheTriggerSession(graphID string, session graphRuntimeSession) graphRuntimeSession {
	if manager == nil || session.runner == nil {
		return session
	}
	graphID = strings.TrimSpace(graphID)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	resolved := manager.rememberSessionLocked(session)
	if existing := manager.triggerSessions[graphID]; existing.runner != nil &&
		strings.TrimSpace(existing.runner.GraphSessionID()) == strings.TrimSpace(resolved.runner.GraphSessionID()) {
		return existing
	}
	manager.triggerSessions[graphID] = resolved
	return resolved
}

func (manager *graphRuntimeManager) cacheSession(session graphRuntimeSession) graphRuntimeSession {
	if manager == nil || session.runner == nil {
		return session
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.rememberSessionLocked(session)
}

func (manager *graphRuntimeManager) session(graphID string, sessionID string) graphRuntimeSession {
	if manager == nil {
		return graphRuntimeSession{}
	}
	key := graphRuntimeSessionKey{
		graphID:   strings.TrimSpace(graphID),
		sessionID: strings.TrimSpace(sessionID),
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.sessions[key]
}

func (manager *graphRuntimeManager) runnerForRun(ctx context.Context, graphID string, runID string) (*runtime.GraphRunner, runtime.RunRecord, error) {
	if manager == nil {
		return nil, runtime.RunRecord{}, nil
	}
	graphID = strings.TrimSpace(graphID)
	runID = strings.TrimSpace(runID)
	manager.mu.RLock()
	candidates := make([]*runtime.GraphRunner, 0, len(manager.sessions)+1)
	seen := make(map[*runtime.GraphRunner]struct{}, len(manager.sessions)+1)
	if manager.current.runner != nil && (graphID == "" || effectiveRunnerGraphID(manager.current.runner) == graphID) {
		candidates = append(candidates, manager.current.runner)
		seen[manager.current.runner] = struct{}{}
	}
	if graphID != "" {
		for key, session := range manager.sessions {
			if key.graphID != graphID || session.runner == nil {
				continue
			}
			if _, exists := seen[session.runner]; exists {
				continue
			}
			seen[session.runner] = struct{}{}
			candidates = append(candidates, session.runner)
		}
	}
	manager.mu.RUnlock()

	for _, runner := range candidates {
		run, err := runner.GetRun(ctx, runID)
		if err == nil {
			if sessionID := strings.TrimSpace(run.GraphSessionID); sessionID != "" && sessionID != strings.TrimSpace(runner.GraphSessionID()) {
				continue
			}
			return runner, run, nil
		}
		if !errors.Is(err, runtime.ErrRunnerRecordNotFound) {
			return nil, runtime.RunRecord{}, err
		}
	}
	return nil, runtime.RunRecord{}, nil
}

func (manager *graphRuntimeManager) activeSessionIDs(graphID string) map[string]struct{} {
	result := make(map[string]struct{})
	if manager == nil {
		return result
	}
	graphID = strings.TrimSpace(graphID)
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	for key, session := range manager.sessions {
		if key.graphID == graphID && session.runner != nil && session.runner.ActiveRunCount() > 0 {
			result[key.sessionID] = struct{}{}
		}
	}
	return result
}

func (manager *graphRuntimeManager) graphActiveState(graphID string) graphActiveState {
	state := graphActiveState{}
	if manager == nil {
		return state
	}
	graphID = strings.TrimSpace(graphID)
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	for key, session := range manager.sessions {
		if key.graphID != graphID || session.runner == nil {
			continue
		}
		count := session.runner.ActiveRunCount()
		if count == 0 {
			continue
		}
		state.ActiveRunCount += count
		state.SessionIDs = append(state.SessionIDs, key.sessionID)
	}
	sort.Strings(state.SessionIDs)
	return state
}

func (manager *graphRuntimeManager) removeSession(graphID string, sessionID string) {
	if manager == nil {
		return
	}
	key := graphRuntimeSessionKey{
		graphID:   strings.TrimSpace(graphID),
		sessionID: strings.TrimSpace(sessionID),
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	delete(manager.sessions, key)
	if latest := manager.triggerSessions[key.graphID]; latest.runner != nil &&
		strings.TrimSpace(latest.runner.GraphSessionID()) == key.sessionID {
		delete(manager.triggerSessions, key.graphID)
	}
}

func (manager *graphRuntimeManager) rememberSessionLocked(session graphRuntimeSession) graphRuntimeSession {
	if session.runner == nil {
		return session
	}
	key := graphRuntimeSessionKey{
		graphID:   effectiveRunnerGraphID(session.runner),
		sessionID: strings.TrimSpace(session.runner.GraphSessionID()),
	}
	if existing := manager.sessions[key]; existing.runner != nil {
		return existing
	}
	manager.sessions[key] = session
	return session
}
