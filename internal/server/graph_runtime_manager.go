package server

import (
	"context"
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

type graphRuntimeManager struct {
	mu              sync.RWMutex
	graphUpdateMu   sync.Mutex
	current         graphRuntimeSession
	defaultContext  context.Context
	defaultSettings graphRuntimeSettings
	triggerSessions map[string]graphRuntimeSession
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
	}
	if runner != nil {
		manager.triggerSessions[effectiveRunnerGraphID(runner)] = manager.current
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

func (manager *graphRuntimeManager) runtimeSettings() graphRuntimeSettings {
	if manager == nil {
		return graphRuntimeSettingsFromContext(context.Background(), "")
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return normalizedGraphSettings(manager.current.settings)
}

func (manager *graphRuntimeManager) defaults() (graphRuntimeSettings, context.Context) {
	if manager == nil {
		return graphRuntimeSettingsFromContext(context.Background(), ""), context.Background()
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
	if existing := manager.triggerSessions[graphID]; existing.runner != nil &&
		strings.TrimSpace(existing.runner.GraphSessionID) == strings.TrimSpace(session.runner.GraphSessionID) {
		return existing
	}
	manager.triggerSessions[graphID] = session
	return session
}
