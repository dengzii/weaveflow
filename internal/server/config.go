package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dengzii/weaveflow/builtin"
	wfgraph "github.com/dengzii/weaveflow/graph"
	wfregistry "github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

type Config struct {
	Graph    *wfgraph.Graph
	Runner   *runtime.GraphRunner
	Registry *wfregistry.Registry

	BaseDir string

	ExecutionStore  runtime.ExecutionStore
	CheckpointStore runtime.CheckpointStore
	ArtifactStore   runtime.ArtifactStore
	EventSink       runtime.EventSink
	Codec           state.StateCodec

	GraphID      string
	GraphVersion string

	EventBuffer int
}

type Server struct {
	mu       sync.RWMutex
	baseCtx  context.Context
	graph    *wfgraph.Graph
	runner   *runtime.GraphRunner
	registry *wfregistry.Registry
	events   *EventHub
	baseDir  string
	cfg      Config
}

func NewServer(ctx context.Context, cfg Config) (*Server, error) {
	return New(ctx, cfg)
}

func New(ctx context.Context, cfg Config) (*Server, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	baseDir := strings.TrimSpace(cfg.BaseDir)
	var err error
	baseDir, err = ensureBaseDir(baseDir)
	if err != nil {
		return nil, err
	}
	cfg.BaseDir = baseDir

	hub := NewEventHub(cfg.EventBuffer)
	runner := cfg.Runner
	if runner == nil && cfg.Graph != nil {
		runner = newDefaultRunner(cfg.Graph, cfg, baseDir)
	}
	attachEventHub(runner, hub)

	reg := cfg.Registry
	if reg == nil {
		reg = builtin.NewDefaultRegistry()
	}

	return &Server{
		baseCtx:  ctx,
		graph:    cfg.Graph,
		runner:   runner,
		registry: reg,
		events:   hub,
		baseDir:  baseDir,
		cfg:      cfg,
	}, nil
}

func ensureBaseDir(baseDir string) (string, error) {
	if strings.TrimSpace(baseDir) == "" {
		return os.MkdirTemp("", "weaveflow-server-*")
	}
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return "", err
	}
	return baseDir, nil
}

func newDefaultRunner(graph *wfgraph.Graph, cfg Config, baseDir string) *runtime.GraphRunner {
	executionStore := cfg.ExecutionStore
	if executionStore == nil {
		executionStore = runtime.NewFileExecutionStore(filepath.Join(baseDir, "execution"))
	}
	checkpointStore := cfg.CheckpointStore
	if checkpointStore == nil {
		checkpointStore = runtime.NewFileCheckpointStore(filepath.Join(baseDir, "checkpoints"))
	}
	codec := cfg.Codec
	if codec == nil {
		codec = state.NewJSONStateCodec("")
	}
	eventSink := cfg.EventSink
	if eventSink == nil {
		eventSink = runtime.NewFileEventSink(filepath.Join(baseDir, "events"))
	}

	runner := wfgraph.NewGraphRunner(graph, executionStore, checkpointStore, codec, eventSink)
	runner.ArtifactStore = cfg.ArtifactStore
	if runner.ArtifactStore == nil {
		runner.ArtifactStore = runtime.NewFileArtifactStore(filepath.Join(baseDir, "artifacts"))
	}
	runner.GraphID = strings.TrimSpace(cfg.GraphID)
	runner.GraphVersion = strings.TrimSpace(cfg.GraphVersion)
	return runner
}

func attachEventHub(runner *runtime.GraphRunner, hub *EventHub) {
	if runner == nil || hub == nil {
		return
	}
	if runner.EventSink == nil {
		runner.EventSink = hub
		return
	}
	if runner.EventSink == hub {
		return
	}
	runner.EventSink = runtime.NewCombineEventSink(runner.EventSink, hub)
}

func (s *Server) BaseDir() string {
	if s == nil {
		return ""
	}
	return s.baseDir
}

func (s *Server) EventHub() *EventHub {
	if s == nil {
		return nil
	}
	return s.events
}

func (s *Server) Runner() *runtime.GraphRunner {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runner
}
