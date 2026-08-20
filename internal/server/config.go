package server

import (
	"context"
	"errors"
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
	"github.com/dengzii/weaveflow/internal/memory"
	filestore "github.com/dengzii/weaveflow/internal/runtimestore/file"
	sqlitestore "github.com/dengzii/weaveflow/internal/runtimestore/sqlite"
	"github.com/dengzii/weaveflow/internal/trigger"
	wfregistry "github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

type RuntimeContextDecorator func(context.Context) context.Context

type Config struct {
	Graph    *wfgraph.Graph
	Registry *wfregistry.Registry

	BaseDir             string
	RuntimeStoreBackend string
	SecretDirectory     string
	SecretResolver      SecretResolver
	ManagementToken     string

	RuntimeContextDecorators []RuntimeContextDecorator
	MemoryStore              memory.Store

	ExecutionStore   runtime.ExecutionStore
	CheckpointStore  runtime.CheckpointStore
	ArtifactStore    runtime.ArtifactStore
	EventSink        runtime.EventSink
	TransactionStore runtime.TransactionStore
	RunDeleter       runtime.RunDeleter
	RunRetention     *runtime.RunRetentionPolicy
	RetentionAudit   runtime.RetentionAuditSink
	Codec            state.Codec

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
	managedSecrets  *managedSecretStore
	secretResolver  SecretResolver
	managementToken string
	memoryStore     memory.Store
}

type graphRuntimeStore struct {
	baseDir string
	store   defaultRuntimeStore
}

const (
	RuntimeStoreFile   = "file"
	RuntimeStoreSQLite = "sqlite"
)

type defaultRuntimeStore interface {
	Close() error
	ExecutionStore() runtime.ExecutionStore
	CheckpointStore() runtime.CheckpointStore
	ArtifactStore() runtime.ArtifactStore
	EventSink() runtime.EventSink
	TransactionStore() runtime.TransactionStore
	ExecutionDeletionStore() runtime.RunDeletionExecutionStore
	CheckpointDeleter() runtime.RunDeleter
	EventDeleter() runtime.RunDeleter
	ArtifactDeleter() runtime.RunDeleter
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
	if cfg.MemoryStore == nil {
		cfg.MemoryStore, err = memory.NewFileStore(filepath.Join(baseDir, "memory", "records.json"))
		if err != nil {
			return nil, err
		}
	}
	externalSecretResolver := cfg.SecretResolver
	if externalSecretResolver == nil {
		externalSecretResolver, err = newLocalSecretResolver(cfg.SecretDirectory)
		if err != nil {
			return nil, err
		}
	}
	managedSecrets, err := newManagedSecretStore(filepath.Join(baseDir, "managed-secrets"))
	if err != nil {
		return nil, err
	}
	secretResolver := &serverSecretResolver{managed: managedSecrets, external: externalSecretResolver}
	cfg.SecretResolver = secretResolver
	cfg.ManagementToken = strings.TrimSpace(cfg.ManagementToken)

	reg := cfg.Registry
	if reg == nil {
		reg = builtin.NewDefaultRegistry()
	}
	cfg.RuntimeContextDecorators = append([]RuntimeContextDecorator(nil), cfg.RuntimeContextDecorators...)
	ctx = memory.WithStore(ctx, cfg.MemoryStore)
	ctx = applyRuntimeContextDecorators(ctx, cfg.RuntimeContextDecorators)
	initialSettings := graphRuntimeSettingsFromContext(ctx)
	ctx = core.WithEnvironment(ctx, initialSettings.Environment)
	hub := NewEventHub(cfg.EventBuffer)
	runtimeManager := newGraphRuntimeManager(ctx, initialSettings, nil, nil)
	var runner *runtime.GraphRunner
	if cfg.Graph != nil {
		runner, err = runtimeManager.newRunner(cfg.Graph, cfg, baseDir, hub)
		if err != nil {
			_ = runtimeManager.Close()
			return nil, err
		}
		runtimeManager.installSession(graphRuntimeSession{
			graph:       cfg.Graph,
			runner:      runner,
			baseContext: ctx,
			settings:    initialSettings,
		})
	}
	srv := &Server{
		runtime:         runtimeManager,
		registry:        reg,
		events:          hub,
		baseDir:         baseDir,
		cfg:             cfg,
		managedSecrets:  managedSecrets,
		secretResolver:  secretResolver,
		managementToken: cfg.ManagementToken,
		memoryStore:     cfg.MemoryStore,
	}
	if err := srv.reconcileCachedRunDeletions(ctx); err != nil {
		_ = srv.Close()
		return nil, fmt.Errorf("reconcile cached run deletions: %w", err)
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
			trigger.WithSecretResolver(secretResolver),
		)
		if err != nil {
			return nil, err
		}
	} else if err := triggerService.SetSecretResolver(secretResolver); err != nil {
		return nil, err
	}
	srv.triggers = triggerService
	srv.chatChannels = triggerService.ChatChannels()
	srv.chatSetup = newChatSetupManager(srv.chatChannels)
	if err := srv.recoverGraphCommits(ctx); err != nil {
		_ = srv.Close()
		return nil, err
	}
	if err := srv.startDurableWorkers(ctx); err != nil {
		_ = srv.Close()
		return nil, err
	}
	return srv, nil
}

func (s *Server) startDurableWorkers(ctx context.Context) error {
	if s == nil || strings.TrimSpace(s.cfg.RuntimeStoreBackend) != RuntimeStoreSQLite {
		return nil
	}
	current := s.runtime.currentSession()
	if current.runner != nil {
		if err := s.runtime.ensureWorker(effectiveRunnerGraphID(current.runner)); err != nil {
			return fmt.Errorf("start durable worker for current graph: %w", err)
		}
	}
	graphs, err := s.listCachedGraphs()
	if err != nil {
		return err
	}
	for _, graph := range graphs {
		if _, err := s.loadTriggerSession(graph.ID); err != nil {
			return fmt.Errorf("load graph %q for durable worker: %w", graph.ID, err)
		}
		if err := s.runtime.ensureWorker(graph.ID); err != nil {
			return fmt.Errorf("start durable worker for graph %q: %w", graph.ID, err)
		}
	}
	return nil
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
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(baseDir, 0o700); err != nil {
		return "", err
	}
	return baseDir, nil
}

func newDefaultRunner(graph *wfgraph.Graph, cfg Config, baseDir string, hub *EventHub) (*runtime.GraphRunner, error) {
	defaultStore, err := openDefaultRuntimeStore(cfg, baseDir)
	if err != nil {
		return nil, err
	}
	runner, err := newRunnerWithStore(graph, cfg, baseDir, hub, defaultStore, true)
	if err != nil && defaultStore != nil {
		_ = defaultStore.Close()
	}
	return runner, err
}

func openDefaultRuntimeStore(cfg Config, baseDir string) (defaultRuntimeStore, error) {
	if !needsDefaultRuntimeStore(cfg) {
		return nil, nil
	}
	switch strings.TrimSpace(cfg.RuntimeStoreBackend) {
	case "", RuntimeStoreFile:
		return filestore.Open(baseDir)
	case RuntimeStoreSQLite:
		return sqlitestore.Open(filepath.Join(baseDir, "runtime.db"))
	default:
		return nil, fmt.Errorf("unsupported runtime store backend %q", cfg.RuntimeStoreBackend)
	}
}

func needsDefaultRuntimeStore(cfg Config) bool {
	return cfg.ExecutionStore == nil || cfg.CheckpointStore == nil || cfg.ArtifactStore == nil || cfg.EventSink == nil
}

func newRunnerWithStore(
	graph *wfgraph.Graph,
	cfg Config,
	baseDir string,
	hub *EventHub,
	defaultStore defaultRuntimeStore,
	closeDefaultStore bool,
) (*runtime.GraphRunner, error) {
	usesDefaultRunStores := cfg.ExecutionStore == nil &&
		cfg.CheckpointStore == nil &&
		cfg.ArtifactStore == nil &&
		cfg.EventSink == nil

	usesDefaultTransactionStores := cfg.ExecutionStore == nil && cfg.CheckpointStore == nil && cfg.EventSink == nil
	if needsDefaultRuntimeStore(cfg) && defaultStore == nil {
		return nil, fmt.Errorf("default runtime store is required")
	}

	executionStore := cfg.ExecutionStore
	var defaultExecutionDeleter runtime.RunDeletionExecutionStore
	if executionStore == nil {
		executionStore = defaultStore.ExecutionStore()
		defaultExecutionDeleter = defaultStore.ExecutionDeletionStore()
	}
	checkpointStore := cfg.CheckpointStore
	var defaultCheckpointDeleter runtime.RunDeleter
	if checkpointStore == nil {
		checkpointStore = defaultStore.CheckpointStore()
		defaultCheckpointDeleter = defaultStore.CheckpointDeleter()
	}
	codec := cfg.Codec
	if codec == nil {
		codec = state.NewJSONStateCodec("")
	}
	eventSink := cfg.EventSink
	var defaultEventDeleter runtime.RunDeleter
	if eventSink == nil {
		eventSink = defaultStore.EventSink()
		defaultEventDeleter = defaultStore.EventDeleter()
	}
	if hub != nil {
		eventSink = runtime.NewCombineEventSink(eventSink, hub)
	}
	var defaultArtifactDeleter runtime.RunDeleter
	artifactStore := cfg.ArtifactStore
	if artifactStore == nil {
		artifactStore = defaultStore.ArtifactStore()
		defaultArtifactDeleter = defaultStore.ArtifactDeleter()
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
	options := []runtime.GraphRunnerOption{
		runtime.WithArtifactStore(artifactStore), runtime.WithRunDeleter(runDeleter),
		runtime.WithRunRetention(retentionPolicy, retentionAudit),
		runtime.WithGraphMetadata(cfg.GraphID, cfg.GraphVersion, cfg.GraphHash, cfg.GraphSnapshotHash, cfg.GraphSessionID),
	}
	transactionStore := cfg.TransactionStore
	if transactionStore == nil && usesDefaultTransactionStores {
		transactionStore = defaultStore.TransactionStore()
	}
	if transactionStore != nil {
		options = append(options, runtime.WithRuntimeTransactionStore(transactionStore))
	}
	if provider, ok := defaultStore.(interface{ TaskQueue() runtime.TaskQueue }); ok {
		options = append(options, runtime.WithTaskQueue(provider.TaskQueue()))
	}
	if closeDefaultStore && defaultStore != nil {
		options = append(options, runtime.WithStoreCloser(defaultStore))
	}
	runner, err := wfgraph.NewGraphRunner(graph, executionStore, checkpointStore, codec, eventSink, options...)
	if err == nil {
		err = runner.ReconcileRunDeletions(context.Background())
	}
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
	return filestore.NewRetentionAuditSink(filepath.Join(baseDir, "retention-audit.jsonl"))
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

func (s *Server) MemoryStore() memory.Store {
	if s == nil {
		return nil
	}
	return s.memoryStore
}

func (s *Server) Start(ctx context.Context) error {
	if s == nil || s.triggers == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.chatSetupSaveMu.Lock()
	defer s.chatSetupSaveMu.Unlock()
	if err := s.sweepManagedSecrets(ctx); err != nil {
		return err
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
	if s.runtime != nil {
		err = errors.Join(err, s.runtime.Close())
	}
	if s.events != nil {
		s.events.Close()
	}
	return err
}
