package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dengzii/weaveflow/builtin"
	wfgraph "github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/internal/chatchannel"
	"github.com/dengzii/weaveflow/internal/chatchannel/wecom"
	"github.com/dengzii/weaveflow/internal/chatchannel/weixin"
	"github.com/dengzii/weaveflow/internal/trigger"
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
	RunDeleter      runtime.RunDeleter
	Codec           state.StateCodec

	GraphID           string
	GraphVersion      string
	GraphHash         string
	GraphSnapshotHash string
	GraphSessionID    string

	EventBuffer int

	TriggerStore   trigger.Store
	TriggerService *trigger.Service
	ChatChannels   *chatchannel.Registry
}

type Server struct {
	runtime         *graphRuntimeManager
	registry        *wfregistry.Registry
	events          *EventHub
	baseDir         string
	cfg             Config
	triggers        *trigger.Service
	chatChannels    *chatchannel.Registry
	chatSetup       *chatSetupManager
	chatSetupSaveMu sync.Mutex
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

	srv := &Server{
		runtime:  newGraphRuntimeManager(ctx, graphRuntimeSettingsFromContext(ctx, baseDir), cfg.Graph, runner),
		registry: reg,
		events:   hub,
		baseDir:  baseDir,
		cfg:      cfg,
	}
	storedSettings, settingsFound, err := loadGraphRuntimeSettings(baseDir)
	if err != nil {
		return nil, err
	}
	if settingsFound {
		apiKey := firstGraphModelAPIKey(storedSettings)
		markGraphModelAPIKeys(&storedSettings, firstNonEmpty(apiKey, os.Getenv("OPENAI_API_KEY")))
		runtimeCtx, err := srv.buildRuntimeContext(storedSettings, apiKey)
		if err != nil {
			return nil, fmt.Errorf("restore graph runtime settings: %w", err)
		}
		if _, err := applyGraphSettingsEnvironment(srv.runtime.runtimeSettings(), storedSettings, apiKey, apiKey != ""); err != nil {
			return nil, fmt.Errorf("restore graph runtime settings environment: %w", err)
		}
		srv.runtime.updateRuntime(storedSettings, runtimeCtx)
	}
	triggerService := cfg.TriggerService
	if triggerService == nil {
		chatChannels := cfg.ChatChannels
		if chatChannels == nil {
			chatChannels = chatchannel.NewDefaultRegistry()
			if err := wecom.Register(chatChannels); err != nil {
				return nil, fmt.Errorf("register WeCom chat channel: %w", err)
			}
			if err := weixin.RegisterWithCursorDirectory(chatChannels, filepath.Join(baseDir, "weixin")); err != nil {
				return nil, fmt.Errorf("register WeChat chat channel: %w", err)
			}
		}
		triggerStore := cfg.TriggerStore
		if triggerStore == nil {
			triggerStore, err = trigger.NewFileStore(filepath.Join(baseDir, "triggers"))
			if err != nil {
				return nil, err
			}
		}
		triggerService, err = trigger.NewService(
			triggerStore,
			trigger.RunnerResolverFunc(srv.resolveTriggerRunner),
			trigger.WithChatChannels(chatChannels),
		)
		if err != nil {
			return nil, err
		}
	}
	srv.triggers = triggerService
	srv.chatChannels = triggerService.ChatChannels()
	srv.chatSetup = newChatSetupManager(srv.chatChannels)
	return srv, nil
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
	usesDefaultRunStores := cfg.ExecutionStore == nil &&
		cfg.CheckpointStore == nil &&
		cfg.ArtifactStore == nil &&
		cfg.EventSink == nil

	executionStore := cfg.ExecutionStore
	var defaultExecutionDeleter runtime.RunDeleter
	if executionStore == nil {
		store := runtime.NewFileExecutionStore(filepath.Join(baseDir, "execution"))
		executionStore = store
		defaultExecutionDeleter = store
	}
	checkpointStore := cfg.CheckpointStore
	var defaultCheckpointDeleter runtime.RunDeleter
	if checkpointStore == nil {
		store := runtime.NewFileCheckpointStore(filepath.Join(baseDir, "checkpoints"))
		checkpointStore = store
		defaultCheckpointDeleter = store
	}
	codec := cfg.Codec
	if codec == nil {
		codec = state.NewJSONStateCodec("")
	}
	eventSink := cfg.EventSink
	var defaultEventDeleter runtime.RunDeleter
	if eventSink == nil {
		store := runtime.NewFileEventSink(filepath.Join(baseDir, "events"))
		eventSink = store
		defaultEventDeleter = store
	}

	runner := wfgraph.NewGraphRunner(graph, executionStore, checkpointStore, codec, eventSink)
	runner.ArtifactStore = cfg.ArtifactStore
	var defaultArtifactDeleter runtime.RunDeleter
	if runner.ArtifactStore == nil {
		store := runtime.NewFileArtifactStore(filepath.Join(baseDir, "artifacts"))
		runner.ArtifactStore = store
		defaultArtifactDeleter = store
	}
	runner.RunDeleter = cfg.RunDeleter
	if runner.RunDeleter == nil && usesDefaultRunStores {
		runner.RunDeleter = runtime.NewRunDeletionCoordinator(
			defaultExecutionDeleter,
			defaultCheckpointDeleter,
			defaultEventDeleter,
			defaultArtifactDeleter,
		)
	}
	runner.GraphID = strings.TrimSpace(cfg.GraphID)
	runner.GraphVersion = strings.TrimSpace(cfg.GraphVersion)
	if graphHash := strings.TrimSpace(cfg.GraphHash); graphHash != "" {
		runner.GraphHash = graphHash
	}
	if graphSnapshotHash := strings.TrimSpace(cfg.GraphSnapshotHash); graphSnapshotHash != "" {
		runner.GraphSnapshotHash = graphSnapshotHash
	}
	if graphSessionID := strings.TrimSpace(cfg.GraphSessionID); graphSessionID != "" {
		runner.GraphSessionID = graphSessionID
	}
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
	if s == nil || s.runtime == nil {
		return nil
	}
	return s.runtime.currentSession().runner
}

func (s *Server) TriggerService() *trigger.Service {
	if s == nil {
		return nil
	}
	return s.triggers
}

func (s *Server) Start(ctx context.Context) error {
	if s == nil || s.triggers == nil {
		return nil
	}
	return s.triggers.Start(ctx)
}

func (s *Server) Close() error {
	if s == nil || s.triggers == nil {
		return nil
	}
	return s.triggers.Close()
}
