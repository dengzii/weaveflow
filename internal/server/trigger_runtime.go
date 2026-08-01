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
	runner, err := s.loadTriggerRunner(graphID)
	if err != nil {
		return nil, err
	}
	return &triggerRunStarter{server: s, runner: runner}, nil
}

// triggerRunStarter binds graph selection to the resolver while taking runtime
// services such as models from the server at the instant the run starts.
type triggerRunStarter struct {
	server *Server
	runner trigger.RunStarter
}

func (s *triggerRunStarter) Start(ctx context.Context, initial *state.State) (runtime.RunRecord, *state.State, error) {
	if s == nil || s.runner == nil {
		return runtime.RunRecord{}, nil, fmt.Errorf("trigger runner is not configured")
	}
	if s.server == nil {
		return s.runner.Start(ctx, initial)
	}
	runCtx, cancel := s.server.deriveRunContextFrom(ctx)
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
	if s.server == nil {
		return asyncRunner.StartAsync(ctx, initial)
	}

	runCtx, cancel := s.server.deriveRunContextFrom(ctx)
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

func (s *Server) loadTriggerRunner(graphID string) (*runtime.GraphRunner, error) {
	session, err := s.latestOfficialGraphSession(graphID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cached := s.runtime.triggerRunner(graphID)
			if cached != nil {
				return cached, nil
			}
			return nil, fmt.Errorf("%w: %q", errTriggerGraphNotFound, graphID)
		}
		return nil, err
	}

	cached := s.runtime.triggerRunner(graphID)
	if cached != nil && strings.TrimSpace(cached.GraphSessionID) == session.manifest.GraphSessionID {
		return cached, nil
	}

	definition, err := wfgraph.LoadGraphDefinitionFile(session.definitionPath)
	if err != nil {
		return nil, err
	}
	registry := s.registry
	if registry == nil {
		registry = builtin.NewDefaultRegistry()
	}
	graph, err := wfgraph.NewBuilder(registry).Build(definition, &wfregistry.BuildContext{})
	if err != nil {
		return nil, err
	}
	graphHash, err := graph.SemanticHash()
	if err != nil {
		return nil, err
	}
	graphSnapshotHash, err := graph.SnapshotHash()
	if err != nil {
		return nil, err
	}
	if expected := strings.TrimSpace(session.manifest.GraphHash); expected != "" && expected != graphHash {
		return nil, fmt.Errorf("graph session %q hash mismatch", session.manifest.GraphSessionID)
	}
	if expected := strings.TrimSpace(session.manifest.GraphSnapshotHash); expected != "" && expected != graphSnapshotHash {
		return nil, fmt.Errorf("graph session %q snapshot hash mismatch", session.manifest.GraphSessionID)
	}

	cfg := s.cfg
	cfg.GraphID = graphID
	cfg.GraphVersion = firstNonEmpty(session.manifest.GraphVersion, metadataString(definition.Metadata, "graph_version"), runtime.DefaultGraphVersion)
	cfg.GraphHash = graphHash
	cfg.GraphSnapshotHash = graphSnapshotHash
	cfg.GraphSessionID = session.manifest.GraphSessionID
	runner := newDefaultRunner(graph, cfg, session.baseDir)
	attachEventHub(runner, s.events)

	return s.runtime.cacheTriggerRunner(graphID, runner), nil
}

func (s *Server) latestOfficialGraphSession(graphID string) (triggerGraphSession, error) {
	type candidate struct {
		graphDir  string
		sessionID string
	}
	var candidates []candidate
	for _, graphDir := range graphStorageDirectories(s.baseDir, graphID) {
		entries, err := os.ReadDir(graphDir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return triggerGraphSession{}, err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				candidates = append(candidates, candidate{graphDir: graphDir, sessionID: entry.Name()})
			}
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].sessionID > candidates[j].sessionID })

	for _, candidate := range candidates {
		manifest, complete, err := readCachedGraphSession(candidate.graphDir, candidate.sessionID)
		if err != nil {
			return triggerGraphSession{}, err
		}
		if !complete || !manifest.Official || manifest.GraphID != graphID {
			continue
		}
		baseDir := filepath.Join(candidate.graphDir, candidate.sessionID)
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
