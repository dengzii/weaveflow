package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dengzii/weaveflow/builtin"
	"github.com/dengzii/weaveflow/core"
	wfgraph "github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/internal/chatchannel"
	"github.com/dengzii/weaveflow/internal/chatchannel/wecom"
	"github.com/dengzii/weaveflow/internal/chatchannel/weixin"
	"github.com/dengzii/weaveflow/internal/trigger"
	wfregistry "github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

type RuntimeContextDecorator func(context.Context) context.Context

type Config struct {
	Graph    *wfgraph.Graph
	Registry *wfregistry.Registry

	BaseDir string

	RuntimeContextDecorators []RuntimeContextDecorator

	ExecutionStore  runtime.ExecutionStore
	CheckpointStore runtime.CheckpointStore
	ArtifactStore   runtime.ArtifactStore
	EventSink       runtime.EventSink
	RunDeleter      runtime.RunDeleter
	RunRetention    *runtime.RunRetentionPolicy
	RetentionAudit  runtime.RetentionAuditSink
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
	var runner *runtime.GraphRunner
	if cfg.Graph != nil {
		var runnerErr error
		runner, runnerErr = newDefaultRunner(cfg.Graph, cfg, baseDir, hub)
		if runnerErr != nil {
			return nil, runnerErr
		}
	}

	reg := cfg.Registry
	if reg == nil {
		reg = builtin.NewDefaultRegistry()
	}
	cfg.RuntimeContextDecorators = append([]RuntimeContextDecorator(nil), cfg.RuntimeContextDecorators...)
	ctx = applyRuntimeContextDecorators(ctx, cfg.RuntimeContextDecorators)
	initialSettings := graphRuntimeSettingsFromContext(ctx)
	ctx = core.WithEnvironment(ctx, initialSettings.Environment)
	srv := &Server{
		runtime:  newGraphRuntimeManager(ctx, initialSettings, cfg.Graph, runner),
		registry: reg,
		events:   hub,
		baseDir:  baseDir,
		cfg:      cfg,
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

func applyRuntimeContextDecorators(ctx context.Context, decorators []RuntimeContextDecorator) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	for _, decorate := range decorators {
		if decorate != nil {
			ctx = decorate(ctx)
		}
	}
	return ctx
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

func newDefaultRunner(graph *wfgraph.Graph, cfg Config, baseDir string, hub *EventHub) (*runtime.GraphRunner, error) {
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
	if hub != nil {
		eventSink = runtime.NewCombineEventSink(eventSink, hub)
	}
	var defaultArtifactDeleter runtime.RunDeleter
	artifactStore := cfg.ArtifactStore
	if artifactStore == nil {
		store := runtime.NewFileArtifactStore(filepath.Join(baseDir, "artifacts"))
		artifactStore = store
		defaultArtifactDeleter = store
	}
	runDeleter := cfg.RunDeleter
	if runDeleter == nil && usesDefaultRunStores {
		runDeleter = runtime.NewRunDeletionCoordinator(
			defaultExecutionDeleter,
			defaultCheckpointDeleter,
			defaultEventDeleter,
			defaultArtifactDeleter,
		)
	}
	retentionPolicy := effectiveRunRetention(cfg.RunRetention, runDeleter != nil)
	var retentionAudit runtime.RetentionAuditSink
	if retentionPolicy.MaxRuns > 0 || retentionPolicy.MaxAge > 0 {
		retentionAudit = effectiveRetentionAudit(cfg.RetentionAudit, baseDir)
	}
	runner, err := wfgraph.NewGraphRunner(graph, executionStore, checkpointStore, codec, eventSink,
		runtime.WithArtifactStore(artifactStore), runtime.WithRunDeleter(runDeleter),
		runtime.WithRunRetention(retentionPolicy, retentionAudit),
		runtime.WithGraphMetadata(cfg.GraphID, cfg.GraphVersion, cfg.GraphHash, cfg.GraphSnapshotHash, cfg.GraphSessionID))
	return runner, err
}

func effectiveRunRetention(policy *runtime.RunRetentionPolicy, deletionConfigured bool) runtime.RunRetentionPolicy {
	if policy != nil {
		return *policy
	}
	if !deletionConfigured {
		return runtime.RunRetentionPolicy{}
	}
	return runtime.RunRetentionPolicy{MaxRuns: 1000, MaxAge: 30 * 24 * time.Hour}
}

func effectiveRetentionAudit(audit runtime.RetentionAuditSink, baseDir string) runtime.RetentionAuditSink {
	if audit != nil {
		return audit
	}
	return runtime.NewFileRetentionAuditSink(filepath.Join(baseDir, "retention-audit.jsonl"))
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
	if s == nil {
		return nil
	}
	var err error
	if s.triggers != nil {
		err = s.triggers.Close()
	}
	if s.events != nil {
		s.events.Close()
	}
	return err
}
