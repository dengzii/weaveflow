// Package weaveflow is the public facade for common graph construction,
// graph loading, runner setup, and runtime inspection APIs.
package weaveflow

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/dengzii/weaveflow/builtin"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"

	"go.uber.org/zap"
)

const EndNodeRef = graph.EndNodeRef

type (
	BuildContext = registry.BuildContext
	Graph        = graph.Graph
	Runnable     = graph.Runnable

	GraphDefinition        = dsl.GraphDefinition
	GraphInstanceConfig    = dsl.GraphInstanceConfig
	GraphNodeSpec          = dsl.GraphNodeSpec
	GraphEdgeSpec          = dsl.GraphEdgeSpec
	GraphConditionSpec     = dsl.GraphConditionSpec
	StateFieldDefinition   = dsl.StateFieldDefinition
	StateContract          = dsl.StateContract
	GraphResolver          = registry.GraphResolver
	Registry               = registry.Registry
	NodeTypeDefinition     = registry.NodeTypeDefinition
	ConditionDefinition    = registry.ConditionDefinition
	EdgeCondition          = registry.EdgeCondition
	EdgeConditionMatcher   = registry.EdgeConditionMatcher
	GraphRunner            = runtime.GraphRunner
	ExecutionStore         = runtime.ExecutionStore
	CheckpointStore        = runtime.CheckpointStore
	EventSink              = runtime.EventSink
	EventReader            = runtime.EventReader
	ArtifactStore          = runtime.ArtifactStore
	FileExecutionStore     = runtime.FileExecutionStore
	FileCheckpointStore    = runtime.FileCheckpointStore
	FileEventSink          = runtime.FileEventSink
	FileArtifactStore      = runtime.FileArtifactStore
	NoopExecutionStore     = runtime.NoopExecutionStore
	NoopCheckpointStore    = runtime.NoopCheckpointStore
	NoopArtifactStore      = runtime.NoopArtifactStore
	NoopEventSink          = runtime.NoopEventSink
	RunStatus              = runtime.RunStatus
	StepStatus             = runtime.StepStatus
	CheckpointStage        = runtime.CheckpointStage
	EventType              = runtime.EventType
	RunRecord              = runtime.RunRecord
	StepRecord             = runtime.StepRecord
	CheckpointRecord       = runtime.CheckpointRecord
	RestoredCheckpoint     = runtime.RestoredCheckpoint
	Artifact               = runtime.Artifact
	Event                  = runtime.Event
	WarningRecord          = runtime.WarningRecord
	RunFilter              = runtime.RunFilter
	Breakpoint             = runtime.Breakpoint
	BreakpointHit          = runtime.BreakpointHit
	RunnerMetadata         = runtime.RunnerMetadata
	ContractPolicy         = runtime.ContractPolicy
	ContractValidationMode = core.ContractValidationMode
	State                  = state.State
	StateCodec             = state.StateCodec
	ArtifactRef            = state.ArtifactRef
	RuntimeState           = state.RuntimeState
)

const (
	RunStatusPending   = runtime.RunStatusPending
	RunStatusRunning   = runtime.RunStatusRunning
	RunStatusPaused    = runtime.RunStatusPaused
	RunStatusFailed    = runtime.RunStatusFailed
	RunStatusCompleted = runtime.RunStatusCompleted
	RunStatusCanceled  = runtime.RunStatusCanceled

	StepStatusScheduled = runtime.StepStatusScheduled
	StepStatusRunning   = runtime.StepStatusRunning
	StepStatusSucceeded = runtime.StepStatusSucceeded
	StepStatusFailed    = runtime.StepStatusFailed
	StepStatusPaused    = runtime.StepStatusPaused

	CheckpointBeforeNode        = runtime.CheckpointBeforeNode
	CheckpointAfterNode         = runtime.CheckpointAfterNode
	CheckpointAfterParallelWave = runtime.CheckpointAfterParallelWave

	EventRunCreated         = runtime.EventRunCreated
	EventRunStarted         = runtime.EventRunStarted
	EventRunPauseRequested  = runtime.EventRunPauseRequested
	EventRunPaused          = runtime.EventRunPaused
	EventRunResumed         = runtime.EventRunResumed
	EventRunCancelRequested = runtime.EventRunCancelRequested
	EventRunCanceled        = runtime.EventRunCanceled
	EventRunFinished        = runtime.EventRunFinished
	EventRunFailed          = runtime.EventRunFailed
	EventNodeStarted        = runtime.EventNodeStarted
	EventNodeFinished       = runtime.EventNodeFinished
	EventNodeFailed         = runtime.EventNodeFailed
	EventNodeRetry          = runtime.EventNodeRetry
	EventNodeCustom         = runtime.EventNodeCustom
	EventLLMReasoningChunk  = runtime.EventLLMReasoningChunk
	EventLLMContentChunk    = runtime.EventLLMContentChunk
	EventLLMReasoning       = runtime.EventLLMReasoning
	EventLLMContent         = runtime.EventLLMContent
	EventLLMFunctionCall    = runtime.EventLLMFunctionCall
	EventLLMUsage           = runtime.EventLLMUsage
	EventLLMCall            = runtime.EventLLMCall
	EventToolStarted        = runtime.EventToolStarted
	EventToolCalled         = runtime.EventToolCalled
	EventToolReturned       = runtime.EventToolReturned
	EventToolFailed         = runtime.EventToolFailed
	EventSubgraphStarted    = runtime.EventSubgraphStarted
	EventSubgraphFinished   = runtime.EventSubgraphFinished
	EventSubgraphFailed     = runtime.EventSubgraphFailed
	EventCheckpointCreated  = runtime.EventCheckpointCreated
	EventArtifactCreated    = runtime.EventArtifactCreated
	EventBreakpointHit      = runtime.EventBreakpointHit
	EventStateChanged       = runtime.EventStateChanged
	EventContractViolation  = runtime.EventContractViolation
	EventWarning            = runtime.EventWarning

	ContractValidationOff    = core.ContractValidationOff
	ContractValidationWarn   = core.ContractValidationWarn
	ContractValidationStrict = core.ContractValidationStrict
)

var ErrRunnerRecordNotFound = runtime.ErrRunnerRecordNotFound

func NewGraph() *Graph { return graph.NewGraph() }

func NewState() *State { return state.NewState() }

func NewJSONStateCodec(version string) StateCodec {
	return state.NewJSONStateCodec(version)
}

func NewDefaultRegistry() *Registry {
	return builtin.NewDefaultRegistry()
}

func NewEdgeCondition(spec GraphConditionSpec, match EdgeConditionMatcher) EdgeCondition {
	return registry.NewEdgeCondition(spec, match)
}

func LoadGraphDefinitionFile(path string) (GraphDefinition, error) {
	return graph.LoadGraphDefinitionFile(path)
}

func BuildGraph(reg *Registry, def GraphDefinition, options ...LoadGraphOption) (*Graph, error) {
	cfg := defaultLoadGraphConfig()
	cfg.registry = reg
	cfg.definition = def
	cfg.hasDefinition = true
	if err := cfg.apply(options); err != nil {
		return nil, err
	}
	return cfg.build()
}

func BuildGraphInstance(reg *Registry, def GraphDefinition, instance GraphInstanceConfig, ctx *BuildContext) (*Graph, error) {
	return graph.BuildGraphInstance(reg, def, instance, ctx)
}

func LoadGraphFromFile(path string, options ...LoadGraphOption) (*Graph, error) {
	def, err := LoadGraphDefinitionFile(path)
	if err != nil {
		return nil, err
	}
	cfg := defaultLoadGraphConfig()
	cfg.definition = def
	cfg.hasDefinition = true
	if err := cfg.apply(options); err != nil {
		return nil, err
	}
	return cfg.build()
}

func NewGraphRunner(g *Graph, es ExecutionStore, cs CheckpointStore, codec StateCodec, sink EventSink) *GraphRunner {
	runner := graph.NewGraphRunner(g, es, cs, codec, sink)
	if runner.ArtifactStore == nil {
		runner.ArtifactStore = NewNoopArtifactStore()
	}
	return runner
}

func NewRunner(g *Graph, options ...RunnerOption) (*GraphRunner, error) {
	cfg := defaultRunnerConfig()
	if err := cfg.apply(options); err != nil {
		return nil, err
	}
	return cfg.build(g)
}

func NewLocalRunner(g *Graph, baseDir string, options ...RunnerOption) (*GraphRunner, error) {
	cfg := defaultRunnerConfig()
	cfg.executionStore = NewFileExecutionStore(baseDir)
	cfg.checkpointStore = NewFileCheckpointStore(filepath.Join(baseDir, "checkpoints"))
	cfg.eventSink = NewFileEventSink(filepath.Join(baseDir, "events"))
	cfg.artifactStore = NewFileArtifactStore(filepath.Join(baseDir, "artifacts"))
	if err := cfg.apply(options); err != nil {
		return nil, err
	}
	return cfg.build(g)
}

func NewInMemoryRunner(g *Graph, options ...RunnerOption) (*GraphRunner, error) {
	cfg := defaultRunnerConfig()
	cfg.executionStore = NewNoopExecutionStore()
	cfg.checkpointStore = NewNoopCheckpointStore()
	cfg.eventSink = NewNoopEventSink()
	cfg.artifactStore = NewNoopArtifactStore()
	if err := cfg.apply(options); err != nil {
		return nil, err
	}
	return cfg.build(g)
}

func NewFileExecutionStore(baseDir string) *FileExecutionStore {
	return runtime.NewFileExecutionStore(baseDir)
}

func NewFileCheckpointStore(baseDir string) *FileCheckpointStore {
	return runtime.NewFileCheckpointStore(baseDir)
}

func NewFileEventSink(baseDir string) *FileEventSink {
	return runtime.NewFileEventSink(baseDir)
}

func NewFileArtifactStore(baseDir string) *FileArtifactStore {
	return runtime.NewFileArtifactStore(baseDir)
}

func NewNoopExecutionStore() *NoopExecutionStore {
	return runtime.NewNoopExecutionStore()
}

func NewNoopCheckpointStore() *NoopCheckpointStore {
	return runtime.NewNoopCheckpointStore()
}

func NewNoopArtifactStore() *NoopArtifactStore {
	return runtime.NewNoopArtifactStore()
}

func NewNoopEventSink() EventSink {
	return runtime.NoopEventSink{}
}

func NewCombineEventSink(sinks ...EventSink) EventSink {
	return runtime.NewCombineEventSink(sinks...)
}

func NewLoggerEventSink(logger *zap.Logger) EventSink {
	return runtime.NewLoggerEventSink(logger)
}

func ContractPolicyForMode(mode ContractValidationMode) ContractPolicy {
	return runtime.ContractPolicyForMode(mode)
}

func IsStreamingEvent(event EventType) bool {
	return runtime.IsStreamingEvent(event)
}

func WithRunnerEventPublisher(ctx context.Context, publisher func(EventType, any) error) context.Context {
	return runtime.WithRunnerEventPublisher(ctx, publisher)
}

func WithRunnerMetadata(ctx context.Context, metadata RunnerMetadata) context.Context {
	return runtime.WithRunnerMetadata(ctx, metadata)
}

func WithRunnerArtifactRecorder(ctx context.Context, recorder func(context.Context, Artifact) (ArtifactRef, error)) context.Context {
	return runtime.WithRunnerArtifactRecorder(ctx, recorder)
}

func PublishRunnerContextEvent(ctx context.Context, eventType EventType, payload any) error {
	return runtime.PublishRunnerContextEvent(ctx, eventType, payload)
}

func SaveArtifact(ctx context.Context, artifact Artifact) (ArtifactRef, error) {
	return runtime.SaveArtifact(ctx, artifact)
}

func SaveJSONArtifact(ctx context.Context, artifactType string, payload any) (ArtifactRef, error) {
	return runtime.SaveJSONArtifact(ctx, artifactType, payload)
}

func SaveArtifactBestEffort(ctx context.Context, artifact Artifact) (ArtifactRef, error) {
	return runtime.SaveArtifactBestEffort(ctx, artifact)
}

func SaveJSONArtifactBestEffort(ctx context.Context, artifactType string, payload any) (ArtifactRef, error) {
	return runtime.SaveJSONArtifactBestEffort(ctx, artifactType, payload)
}

func SetLogger(l *zap.Logger) { runtime.SetLogger(l) }

type LoadGraphOption interface {
	applyLoadGraphOption(*loadGraphConfig) error
}

type loadGraphOptionFunc func(*loadGraphConfig) error

func (f loadGraphOptionFunc) applyLoadGraphOption(cfg *loadGraphConfig) error {
	return f(cfg)
}

func WithRegistry(reg *Registry) LoadGraphOption {
	return loadGraphOptionFunc(func(cfg *loadGraphConfig) error {
		if reg == nil {
			return fmt.Errorf("registry is nil")
		}
		cfg.registry = reg
		return nil
	})
}

func WithBuildContext(ctx *BuildContext) LoadGraphOption {
	return loadGraphOptionFunc(func(cfg *loadGraphConfig) error {
		if ctx == nil {
			cfg.buildContext = &BuildContext{}
			return nil
		}
		cfg.buildContext = ctx.Clone()
		return nil
	})
}

func WithGraphResolver(resolver GraphResolver) LoadGraphOption {
	return loadGraphOptionFunc(func(cfg *loadGraphConfig) error {
		cfg.buildContext.GraphResolver = resolver
		return nil
	})
}

func WithInstanceConfig(config GraphInstanceConfig) LoadGraphOption {
	return loadGraphOptionFunc(func(cfg *loadGraphConfig) error {
		cfg.instanceConfig = &config
		return nil
	})
}

type loadGraphConfig struct {
	registry       *Registry
	buildContext   *BuildContext
	definition     GraphDefinition
	hasDefinition  bool
	instanceConfig *GraphInstanceConfig
}

func defaultLoadGraphConfig() loadGraphConfig {
	return loadGraphConfig{
		registry:     NewDefaultRegistry(),
		buildContext: &BuildContext{},
	}
}

func (cfg *loadGraphConfig) apply(options []LoadGraphOption) error {
	if cfg.buildContext == nil {
		cfg.buildContext = &BuildContext{}
	}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option.applyLoadGraphOption(cfg); err != nil {
			return err
		}
		if cfg.buildContext == nil {
			cfg.buildContext = &BuildContext{}
		}
	}
	return nil
}

func (cfg loadGraphConfig) build() (*Graph, error) {
	if cfg.registry == nil {
		return nil, fmt.Errorf("registry is nil")
	}
	if !cfg.hasDefinition {
		return nil, fmt.Errorf("graph definition is required")
	}
	if cfg.buildContext == nil {
		cfg.buildContext = &BuildContext{}
	}
	if cfg.instanceConfig != nil {
		return graph.BuildGraphInstance(cfg.registry, cfg.definition, *cfg.instanceConfig, cfg.buildContext)
	}
	return graph.BuildGraph(cfg.registry, cfg.definition, cfg.buildContext)
}

type RunnerOption interface {
	applyRunnerOption(*runnerConfig) error
}

type runnerOptionFunc func(*runnerConfig) error

func (f runnerOptionFunc) applyRunnerOption(cfg *runnerConfig) error {
	return f(cfg)
}

func WithExecutionStore(store ExecutionStore) RunnerOption {
	return runnerOptionFunc(func(cfg *runnerConfig) error {
		if store == nil {
			return fmt.Errorf("execution store is nil")
		}
		cfg.executionStore = store
		return nil
	})
}

func WithCheckpointStore(store CheckpointStore) RunnerOption {
	return runnerOptionFunc(func(cfg *runnerConfig) error {
		if store == nil {
			return fmt.Errorf("checkpoint store is nil")
		}
		cfg.checkpointStore = store
		return nil
	})
}

func WithEventSink(sink EventSink) RunnerOption {
	return runnerOptionFunc(func(cfg *runnerConfig) error {
		if sink == nil {
			return fmt.Errorf("event sink is nil")
		}
		cfg.eventSink = sink
		return nil
	})
}

func WithArtifactStore(store ArtifactStore) RunnerOption {
	return runnerOptionFunc(func(cfg *runnerConfig) error {
		if store == nil {
			return fmt.Errorf("artifact store is nil")
		}
		cfg.artifactStore = store
		return nil
	})
}

func WithStateCodec(codec StateCodec) RunnerOption {
	return runnerOptionFunc(func(cfg *runnerConfig) error {
		if codec == nil {
			return fmt.Errorf("state codec is nil")
		}
		cfg.codec = codec
		return nil
	})
}

func WithGraphID(id string) RunnerOption {
	return runnerOptionFunc(func(cfg *runnerConfig) error {
		cfg.graphID = id
		return nil
	})
}

func WithGraphVersion(version string) RunnerOption {
	return runnerOptionFunc(func(cfg *runnerConfig) error {
		cfg.graphVersion = version
		return nil
	})
}

func WithBreakpoints(breakpoints ...Breakpoint) RunnerOption {
	return runnerOptionFunc(func(cfg *runnerConfig) error {
		cfg.breakpoints = append([]Breakpoint(nil), breakpoints...)
		return nil
	})
}

func WithContractValidation(mode ContractValidationMode) RunnerOption {
	return runnerOptionFunc(func(cfg *runnerConfig) error {
		cfg.contractValidation = mode
		return nil
	})
}

func WithContractPolicy(policy ContractPolicy) RunnerOption {
	return runnerOptionFunc(func(cfg *runnerConfig) error {
		cfg.contractPolicy = policy
		return nil
	})
}

func WithNow(now func() time.Time) RunnerOption {
	return runnerOptionFunc(func(cfg *runnerConfig) error {
		if now == nil {
			return fmt.Errorf("now function is nil")
		}
		cfg.now = now
		return nil
	})
}

type runnerConfig struct {
	executionStore     ExecutionStore
	checkpointStore    CheckpointStore
	eventSink          EventSink
	artifactStore      ArtifactStore
	codec              StateCodec
	graphID            string
	graphVersion       string
	breakpoints        []Breakpoint
	contractValidation ContractValidationMode
	contractPolicy     ContractPolicy
	now                func() time.Time
}

func defaultRunnerConfig() runnerConfig {
	return runnerConfig{
		executionStore:  NewNoopExecutionStore(),
		checkpointStore: NewNoopCheckpointStore(),
		eventSink:       NewNoopEventSink(),
		artifactStore:   NewNoopArtifactStore(),
		codec:           NewJSONStateCodec(""),
	}
}

func (cfg *runnerConfig) apply(options []RunnerOption) error {
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option.applyRunnerOption(cfg); err != nil {
			return err
		}
	}
	return nil
}

func (cfg runnerConfig) build(g *Graph) (*GraphRunner, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}
	if cfg.executionStore == nil {
		return nil, fmt.Errorf("execution store is nil")
	}
	if cfg.checkpointStore == nil {
		return nil, fmt.Errorf("checkpoint store is nil")
	}
	if cfg.eventSink == nil {
		cfg.eventSink = NewNoopEventSink()
	}
	if cfg.artifactStore == nil {
		cfg.artifactStore = NewNoopArtifactStore()
	}
	if cfg.codec == nil {
		cfg.codec = NewJSONStateCodec("")
	}
	runner := NewGraphRunner(g, cfg.executionStore, cfg.checkpointStore, cfg.codec, cfg.eventSink)
	runner.ArtifactStore = cfg.artifactStore
	runner.GraphID = cfg.graphID
	runner.GraphVersion = cfg.graphVersion
	runner.Breakpoints = append([]Breakpoint(nil), cfg.breakpoints...)
	runner.ContractValidation = cfg.contractValidation
	runner.ContractPolicy = cfg.contractPolicy
	if cfg.now != nil {
		runner.Now = cfg.now
	}
	return runner, nil
}
