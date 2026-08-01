package server

import (
	"context"
	"strings"
	"sync"

	wfgraph "github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/runtime"
)

type graphRuntimeSession struct {
	graph    *wfgraph.Graph
	runner   *runtime.GraphRunner
	official bool
}

type graphRuntimeManager struct {
	mu             sync.RWMutex
	graphUpdateMu  sync.Mutex
	settingsUpdate sync.Mutex
	current        graphRuntimeSession
	baseContext    context.Context
	settings       graphRuntimeSettings
	triggerRunners map[string]*runtime.GraphRunner
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
			graph:  graph,
			runner: runner,
		},
		baseContext:    baseContext,
		settings:       settings,
		triggerRunners: make(map[string]*runtime.GraphRunner),
	}
	if runner != nil {
		manager.triggerRunners[effectiveRunnerGraphID(runner)] = runner
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
	if session.official && session.runner != nil {
		manager.triggerRunners[effectiveRunnerGraphID(session.runner)] = session.runner
	}
}

func (manager *graphRuntimeManager) promoteCurrentSession(graphID string, runner *runtime.GraphRunner) {
	if manager == nil || runner == nil {
		return
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.current.runner != runner {
		return
	}
	manager.current.official = true
	manager.triggerRunners[strings.TrimSpace(graphID)] = runner
}

func (manager *graphRuntimeManager) runtimeContext() context.Context {
	if manager == nil {
		return context.Background()
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	if manager.baseContext == nil {
		return context.Background()
	}
	return manager.baseContext
}

func (manager *graphRuntimeManager) runtimeSettings() graphRuntimeSettings {
	if manager == nil {
		return graphRuntimeSettingsFromContext(context.Background(), "")
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return sanitizedGraphSettings(manager.settings)
}

func (manager *graphRuntimeManager) updateRuntime(settings graphRuntimeSettings, baseContext context.Context) {
	if manager == nil {
		return
	}
	if baseContext == nil {
		baseContext = context.Background()
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.settings = sanitizedGraphSettings(settings)
	manager.baseContext = baseContext
}

func (manager *graphRuntimeManager) triggerRunner(graphID string) *runtime.GraphRunner {
	if manager == nil {
		return nil
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.triggerRunners[strings.TrimSpace(graphID)]
}

func (manager *graphRuntimeManager) cacheTriggerRunner(graphID string, runner *runtime.GraphRunner) *runtime.GraphRunner {
	if manager == nil || runner == nil {
		return runner
	}
	graphID = strings.TrimSpace(graphID)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if existing := manager.triggerRunners[graphID]; existing != nil &&
		strings.TrimSpace(existing.GraphSessionID) == strings.TrimSpace(runner.GraphSessionID) {
		return existing
	}
	manager.triggerRunners[graphID] = runner
	return runner
}
