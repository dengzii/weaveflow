package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dengzii/weaveflow/builtin"
	chatcap "github.com/dengzii/weaveflow/capability/chat"
	wfgraph "github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/internal/trigger"
	wfregistry "github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

type triggerGraphSession struct {
	baseDir        string
	definitionPath string
	manifest       graphSessionManifest
}

func (s *Server) defaultTriggerTarget() trigger.Target {
	runner := s.currentRunner()
	if runner == nil {
		return trigger.Target{}
	}
	return trigger.Target{GraphID: effectiveRunnerGraphID(runner)}
}

func (s *Server) resolveTriggerRunner(_ context.Context, target trigger.Target) (trigger.RunStarter, error) {
	graphID := strings.TrimSpace(target.GraphID)
	if graphID == "" {
		return nil, fmt.Errorf("%w: graph_id is required", trigger.ErrInvalidTarget)
	}
	session, err := s.loadTriggerSession(graphID)
	if err != nil {
		return nil, err
	}
	return &triggerRunStarter{baseContext: session.baseContext, runner: session.runner}, nil
}

// triggerRunStarter keeps graph execution and runtime settings on the same
// immutable uploaded session.
type triggerRunStarter struct {
	baseContext context.Context
	runner      trigger.RunStarter
}

func (s *triggerRunStarter) Start(ctx context.Context, initial *state.State) (runtime.RunRecord, *state.State, error) {
	if s == nil || s.runner == nil {
		return runtime.RunRecord{}, nil, fmt.Errorf("trigger runner is not configured")
	}
	runCtx, cancel := deriveRunContextFromBase(ctx, s.baseContext)
	defer cancel()
	if sink := chatcap.ReplySinkFromContext(ctx); sink != nil {
		runCtx = chatcap.WithReplySink(runCtx, sink)
	}
	if observer := runtime.RunnerEventObserverFromContext(ctx); observer != nil {
		runCtx = runtime.WithRunnerEventObserver(runCtx, observer)
	}
	return s.runner.Start(runCtx, initial)
}

func (s *triggerRunStarter) StartAsync(ctx context.Context, initial *state.State) (runtime.RunRecord, <-chan struct{}, error) {
	if s == nil || s.runner == nil {
		return runtime.RunRecord{}, nil, fmt.Errorf("trigger runner is not configured")
	}
	asyncRunner, ok := s.runner.(trigger.AsyncRunStarter)
	if !ok {
		run, _, err := s.Start(ctx, initial)
		done := make(chan struct{})
		close(done)
		return run, done, err
	}
	runCtx, cancel := deriveRunContextFromBase(ctx, s.baseContext)
	if sink := chatcap.ReplySinkFromContext(ctx); sink != nil {
		runCtx = chatcap.WithReplySink(runCtx, sink)
	}
	if observer := runtime.RunnerEventObserverFromContext(ctx); observer != nil {
		runCtx = runtime.WithRunnerEventObserver(runCtx, observer)
	}
	run, innerDone, err := asyncRunner.StartAsync(runCtx, initial)
	if err != nil {
		cancel()
		return runtime.RunRecord{}, nil, err
	}
	done := make(chan struct{})
	go func() {
		if innerDone != nil {
			<-innerDone
		}
		cancel()
		close(done)
	}()
	return run, done, nil
}

func triggerTargetMatchesRunner(graphID string, runner *runtime.GraphRunner) bool {
	return runner != nil && strings.TrimSpace(graphID) == effectiveRunnerGraphID(runner)
}

func (s *Server) loadTriggerSession(graphID string) (graphRuntimeSession, error) {
	session, err := s.latestGraphSession(graphID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cached := s.runtime.triggerSession(graphID)
			if cached.runner != nil {
				return cached, nil
			}
			return graphRuntimeSession{}, fmt.Errorf("%w: %q", errTriggerGraphNotFound, graphID)
		}
		return graphRuntimeSession{}, err
	}

	cached := s.runtime.triggerSession(graphID)
	if cached.runner != nil && strings.TrimSpace(cached.runner.GraphSessionID) == session.manifest.GraphSessionID {
		return cached, nil
	}

	definition, err := wfgraph.LoadGraphDefinitionFile(session.definitionPath)
	if err != nil {
		return graphRuntimeSession{}, err
	}
	registry := s.registry
	if registry == nil {
		registry = builtin.NewDefaultRegistry()
	}
	graph, err := wfgraph.NewBuilder(registry).Build(definition, &wfregistry.BuildContext{})
	if err != nil {
		return graphRuntimeSession{}, err
	}
	graphHash, err := graph.SemanticHash()
	if err != nil {
		return graphRuntimeSession{}, err
	}
	graphSnapshotHash, err := graph.SnapshotHash()
	if err != nil {
		return graphRuntimeSession{}, err
	}
	if session.manifest.GraphHash != graphHash {
		return graphRuntimeSession{}, fmt.Errorf("graph session %q hash mismatch", session.manifest.GraphSessionID)
	}
	if session.manifest.GraphSnapshotHash != graphSnapshotHash {
		return graphRuntimeSession{}, fmt.Errorf("graph session %q snapshot hash mismatch", session.manifest.GraphSessionID)
	}
	settings, found, err := loadGraphRuntimeSettings(session.baseDir)
	if err != nil {
		return graphRuntimeSession{}, err
	}
	if !found {
		return graphRuntimeSession{}, fmt.Errorf("graph session %q settings are missing", session.manifest.GraphSessionID)
	}
	apiKey := firstNonEmpty(firstGraphModelAPIKey(settings), settings.Environment["OPENAI_API_KEY"], os.Getenv("OPENAI_API_KEY"))
	markGraphModelAPIKeys(&settings, apiKey)
	baseContext, err := s.buildRuntimeContext(settings, apiKey)
	if err != nil {
		return graphRuntimeSession{}, err
	}

	cfg := s.cfg
	cfg.GraphID = graphID
	cfg.GraphVersion = session.manifest.GraphVersion
	cfg.GraphHash = graphHash
	cfg.GraphSnapshotHash = graphSnapshotHash
	cfg.GraphSessionID = session.manifest.GraphSessionID
	runner := newDefaultRunner(graph, cfg, session.baseDir)
	attachEventHub(runner, s.events)

	return s.runtime.cacheTriggerSession(graphID, graphRuntimeSession{
		graph:       graph,
		runner:      runner,
		baseContext: baseContext,
		settings:    settings,
	}), nil
}

func (s *Server) latestGraphSession(graphID string) (triggerGraphSession, error) {
	var candidates []string
	graphDir := graphStorageDirectory(s.baseDir, graphID)
	entries, err := os.ReadDir(graphDir)
	if err != nil && !os.IsNotExist(err) {
		return triggerGraphSession{}, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			candidates = append(candidates, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(candidates)))

	for _, sessionID := range candidates {
		manifest, complete, err := readCachedGraphSession(graphDir, sessionID)
		if err != nil {
			return triggerGraphSession{}, err
		}
		if !complete || manifest.GraphID != graphID {
			continue
		}
		baseDir := filepath.Join(graphDir, sessionID)
		definitionName := manifest.DefinitionPath
		definitionPath := filepath.Join(baseDir, definitionName)
		return triggerGraphSession{
			baseDir:        baseDir,
			definitionPath: definitionPath,
			manifest:       manifest,
		}, nil
	}
	return triggerGraphSession{}, os.ErrNotExist
}
