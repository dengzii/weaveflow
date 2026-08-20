package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dengzii/weaveflow/core"
	wfgraph "github.com/dengzii/weaveflow/graph"
	workerpkg "github.com/dengzii/weaveflow/internal/worker"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/google/uuid"
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
	stores          map[string]graphRuntimeStore
	workers         map[string]context.CancelFunc
	workerDone      map[string]<-chan struct{}
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
		stores:          make(map[string]graphRuntimeStore),
		workers:         make(map[string]context.CancelFunc),
		workerDone:      make(map[string]<-chan struct{}),
	}
	if runner != nil {
		manager.triggerSessions[effectiveRunnerGraphID(runner)] = manager.current
		manager.rememberSessionLocked(manager.current)
	}
	return manager
}

func (manager *graphRuntimeManager) taskQueue(graphID string) runtime.AtomicTaskQueue {
	if manager == nil {
		return nil
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	stored := manager.stores[strings.TrimSpace(graphID)]
	provider, ok := stored.store.(interface{ TaskQueue() runtime.TaskQueue })
	if !ok {
		return nil
	}
	queue, _ := provider.TaskQueue().(runtime.AtomicTaskQueue)
	return queue
}

func (manager *graphRuntimeManager) ensureWorker(graphID string) error {
	graphID = strings.TrimSpace(graphID)
	manager.mu.Lock()
	if _, exists := manager.workers[graphID]; exists {
		manager.mu.Unlock()
		return nil
	}
	stored := manager.stores[graphID]
	provider, ok := stored.store.(interface{ TaskQueue() runtime.TaskQueue })
	if !ok {
		manager.mu.Unlock()
		return nil
	}
	queue, ok := provider.TaskQueue().(runtime.AtomicTaskQueue)
	if !ok {
		manager.mu.Unlock()
		return errors.New("runtime store task queue is not atomic")
	}
	baseContext := manager.defaultContext
	if baseContext == nil {
		baseContext = context.Background()
	}
	workerContext, cancel := context.WithCancel(baseContext)
	durableWorker, err := workerpkg.New(queue, graphRunTaskHandler{manager: manager}, workerpkg.Config{
		Identity: runtime.WorkerIdentity{ID: "server-" + uuid.NewString()}, Kinds: []string{runtime.TaskKindGraphRun},
		LeaseTTL: 30 * time.Second, HeartbeatInterval: 10 * time.Second, PollInterval: 250 * time.Millisecond,
	})
	if err != nil {
		cancel()
		manager.mu.Unlock()
		return err
	}
	done := make(chan struct{})
	manager.workers[graphID] = cancel
	manager.workerDone[graphID] = done
	manager.mu.Unlock()
	go func() {
		defer close(done)
		_ = durableWorker.Run(workerContext)
	}()
	return nil
}

type graphRunTaskHandler struct {
	manager *graphRuntimeManager
}

func (handler graphRunTaskHandler) HandleTask(ctx context.Context, task runtime.Task) (runtime.TaskResult, error) {
	session := handler.manager.session(task.GraphID, task.GraphSessionID)
	if session.runner == nil {
		return runtime.TaskResult{}, fmt.Errorf("graph session %q for task %q is not loaded", task.GraphSessionID, task.ID)
	}
	run, err := session.runner.GetRun(ctx, task.RunID)
	if err != nil {
		return runtime.TaskResult{}, err
	}
	if run.Status == runtime.RunStatusFailed {
		steps, err := session.runner.ListSteps(ctx, run.RunID)
		if err != nil {
			return runtime.TaskResult{}, err
		}
		for _, step := range steps {
			if step.EffectStatus == core.EffectUnknown {
				return runtime.TaskResult{}, fmt.Errorf("%w: run %q step %q has an unresolved effect", runtime.ErrRunControlNotAllowed, run.RunID, step.StepID)
			}
		}
	}
	if !isTerminalRuntimeStatus(run.Status) {
		runContext, cancel := deriveRunContextFromBase(ctx, session.baseContext)
		defer cancel()
		resumed, _, resumeErr := session.runner.Resume(runContext, task.RunID, nil)
		if resumed.RunID != "" {
			run = resumed
		}
		if resumeErr != nil && !isTerminalRuntimeStatus(run.Status) {
			return runtime.TaskResult{}, resumeErr
		}
	}
	payload, err := json.Marshal(map[string]any{"run_id": run.RunID, "status": run.Status})
	if err != nil {
		return runtime.TaskResult{}, err
	}
	return runtime.TaskResult{Payload: payload}, nil
}

func (handler graphRunTaskHandler) RetryTask(err error) bool {
	return errors.Is(err, runtime.ErrExecutionLeaseHeld) || errors.Is(err, runtime.ErrRunRevisionConflict)
}

func isTerminalRuntimeStatus(status runtime.RunStatus) bool {
	switch status {
	case runtime.RunStatusCompleted, runtime.RunStatusFailed, runtime.RunStatusCanceled:
		return true
	default:
		return false
	}
}

func (manager *graphRuntimeManager) newRunner(
	graph *wfgraph.Graph,
	cfg Config,
	baseDir string,
	hub *EventHub,
) (*runtime.GraphRunner, error) {
	if manager == nil {
		return nil, fmt.Errorf("graph runtime manager is required")
	}
	defaultStore, err := manager.defaultStore(cfg.GraphID, baseDir, cfg)
	if err != nil {
		return nil, err
	}
	return newRunnerWithStore(graph, cfg, baseDir, hub, defaultStore, false)
}

func (manager *graphRuntimeManager) defaultStore(
	graphID string,
	baseDir string,
	cfg Config,
) (defaultRuntimeStore, error) {
	if !needsDefaultRuntimeStore(cfg) {
		return nil, nil
	}
	graphID = strings.TrimSpace(graphID)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if existing, ok := manager.stores[graphID]; ok {
		if existing.baseDir != baseDir {
			return nil, fmt.Errorf("graph %q runtime store directory changed from %q to %q", graphID, existing.baseDir, baseDir)
		}
		return existing.store, nil
	}
	store, err := openDefaultRuntimeStore(cfg, baseDir)
	if err != nil {
		return nil, err
	}
	manager.stores[graphID] = graphRuntimeStore{baseDir: baseDir, store: store}
	return store, nil
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

func (manager *graphRuntimeManager) refreshSession(session graphRuntimeSession) {
	if manager == nil || session.runner == nil {
		return
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	key := graphRuntimeSessionKey{
		graphID:   effectiveRunnerGraphID(session.runner),
		sessionID: strings.TrimSpace(session.runner.GraphSessionID()),
	}
	manager.current = session
	manager.triggerSessions[key.graphID] = session
	manager.sessions[key] = session
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
	session := manager.sessions[key]
	delete(manager.sessions, key)
	if latest := manager.triggerSessions[key.graphID]; latest.runner != nil &&
		strings.TrimSpace(latest.runner.GraphSessionID()) == key.sessionID {
		if session.runner == nil {
			session = latest
		}
		delete(manager.triggerSessions, key.graphID)
	}
	manager.mu.Unlock()
	if session.runner != nil {
		_ = session.runner.Close()
	}
}

func (manager *graphRuntimeManager) Close() error {
	if manager == nil {
		return nil
	}
	manager.mu.Lock()
	workerCancels := make([]context.CancelFunc, 0, len(manager.workers))
	workerDone := make([]<-chan struct{}, 0, len(manager.workerDone))
	for _, cancel := range manager.workers {
		workerCancels = append(workerCancels, cancel)
	}
	for _, done := range manager.workerDone {
		workerDone = append(workerDone, done)
	}
	manager.workers = make(map[string]context.CancelFunc)
	manager.workerDone = make(map[string]<-chan struct{})
	manager.mu.Unlock()
	for _, cancel := range workerCancels {
		cancel()
	}
	for _, done := range workerDone {
		<-done
	}
	manager.mu.Lock()
	runners := make(map[*runtime.GraphRunner]struct{}, len(manager.sessions)+len(manager.triggerSessions)+1)
	if manager.current.runner != nil {
		runners[manager.current.runner] = struct{}{}
	}
	for _, session := range manager.sessions {
		if session.runner != nil {
			runners[session.runner] = struct{}{}
		}
	}
	for _, session := range manager.triggerSessions {
		if session.runner != nil {
			runners[session.runner] = struct{}{}
		}
	}
	for runner := range runners {
		if active := runner.ActiveRunCount(); active > 0 {
			manager.mu.Unlock()
			return fmt.Errorf("cannot close graph runtime manager with %d active executions", active)
		}
	}
	stores := make([]defaultRuntimeStore, 0, len(manager.stores))
	for _, runtimeStore := range manager.stores {
		if runtimeStore.store != nil {
			stores = append(stores, runtimeStore.store)
		}
	}
	manager.current = graphRuntimeSession{}
	manager.triggerSessions = make(map[string]graphRuntimeSession)
	manager.sessions = make(map[graphRuntimeSessionKey]graphRuntimeSession)
	manager.stores = make(map[string]graphRuntimeStore)
	manager.mu.Unlock()

	var result error
	for runner := range runners {
		result = errors.Join(result, runner.Close())
	}
	for _, store := range stores {
		result = errors.Join(result, store.Close())
	}
	return result
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
