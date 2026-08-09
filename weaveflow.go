// Package weaveflow provides high-level graph construction, loading, and
// runner assembly. Domain types and lower-level operations remain owned by
// their respective core, dsl, graph, registry, runtime, and state packages.
package weaveflow

import (
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
)

const EndNodeRef = graph.EndNodeRef

func NewGraph() *graph.Graph {
	return graph.NewGraph(NewDefaultRegistry())
}

func NewDefaultRegistry() *registry.Registry {
	return builtin.NewDefaultRegistry()
}

func BuildGraph(reg *registry.Registry, definition dsl.GraphDefinition, options ...LoadGraphOption) (*graph.Graph, error) {
	config := defaultLoadGraphConfig()
	config.registry = reg
	config.definition = definition
	config.hasDefinition = true
	if err := config.apply(options); err != nil {
		return nil, err
	}
	return config.build()
}

func BuildGraphInstance(reg *registry.Registry, definition dsl.GraphDefinition, instance dsl.GraphInstanceConfig, buildContext *registry.BuildContext) (*graph.Graph, error) {
	if reg == nil {
		return nil, fmt.Errorf("registry is required")
	}
	return graph.NewBuilder(reg).BuildInstance(definition, instance, buildContext)
}

func LoadGraphFromFile(path string, options ...LoadGraphOption) (*graph.Graph, error) {
	definition, err := graph.LoadGraphDefinitionFile(path)
	if err != nil {
		return nil, err
	}
	config := defaultLoadGraphConfig()
	config.definition = definition
	config.hasDefinition = true
	if err := config.apply(options); err != nil {
		return nil, err
	}
	return config.build()
}

type LoadGraphOption interface {
	applyLoadGraphOption(*loadGraphConfig) error
}

type loadGraphOptionFunc func(*loadGraphConfig) error

func (option loadGraphOptionFunc) applyLoadGraphOption(config *loadGraphConfig) error {
	return option(config)
}

func WithRegistry(reg *registry.Registry) LoadGraphOption {
	return loadGraphOptionFunc(func(config *loadGraphConfig) error {
		if reg == nil {
			return fmt.Errorf("registry is required")
		}
		config.registry = reg
		return nil
	})
}

func WithBuildContext(buildContext *registry.BuildContext) LoadGraphOption {
	return loadGraphOptionFunc(func(config *loadGraphConfig) error {
		if buildContext == nil {
			config.buildContext = &registry.BuildContext{}
			return nil
		}
		config.buildContext = buildContext.Clone()
		return nil
	})
}

func WithGraphResolver(resolver registry.GraphResolver) LoadGraphOption {
	return loadGraphOptionFunc(func(config *loadGraphConfig) error {
		config.ensureBuildContext()
		config.buildContext.GraphResolver = resolver
		return nil
	})
}

func WithInstanceConfig(instance dsl.GraphInstanceConfig) LoadGraphOption {
	return loadGraphOptionFunc(func(config *loadGraphConfig) error {
		cloned := instance
		config.instanceConfig = &cloned
		return nil
	})
}

type loadGraphConfig struct {
	registry       *registry.Registry
	buildContext   *registry.BuildContext
	definition     dsl.GraphDefinition
	hasDefinition  bool
	instanceConfig *dsl.GraphInstanceConfig
}

func defaultLoadGraphConfig() loadGraphConfig {
	return loadGraphConfig{
		registry:     NewDefaultRegistry(),
		buildContext: &registry.BuildContext{},
	}
}

func (config *loadGraphConfig) apply(options []LoadGraphOption) error {
	config.ensureBuildContext()
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option.applyLoadGraphOption(config); err != nil {
			return err
		}
		config.ensureBuildContext()
	}
	return nil
}

func (config *loadGraphConfig) ensureBuildContext() {
	if config.buildContext == nil {
		config.buildContext = &registry.BuildContext{}
	}
}

func (config *loadGraphConfig) build() (*graph.Graph, error) {
	if config.registry == nil {
		return nil, fmt.Errorf("registry is required")
	}
	if !config.hasDefinition {
		return nil, fmt.Errorf("graph definition is required")
	}
	config.ensureBuildContext()
	builder := graph.NewBuilder(config.registry)
	if config.instanceConfig != nil {
		return builder.BuildInstance(config.definition, *config.instanceConfig, config.buildContext)
	}
	return builder.Build(config.definition, config.buildContext)
}

func NewRunner(target *graph.Graph, options ...RunnerOption) (*runtime.GraphRunner, error) {
	config := defaultRunnerConfig()
	if err := config.apply(options); err != nil {
		return nil, err
	}
	return config.build(target)
}

func NewLocalRunner(target *graph.Graph, baseDir string, options ...RunnerOption) (*runtime.GraphRunner, error) {
	if baseDir == "" {
		return nil, fmt.Errorf("runner base directory is required")
	}
	config := defaultRunnerConfig()
	config.executionStore = runtime.NewFileExecutionStore(baseDir)
	config.checkpointStore = runtime.NewFileCheckpointStore(filepath.Join(baseDir, "checkpoints"))
	config.eventSink = runtime.NewFileEventSink(filepath.Join(baseDir, "events"))
	config.artifactStore = runtime.NewFileArtifactStore(filepath.Join(baseDir, "artifacts"))
	if err := config.apply(options); err != nil {
		return nil, err
	}
	return config.build(target)
}

func NewInMemoryRunner(target *graph.Graph, options ...RunnerOption) (*runtime.GraphRunner, error) {
	config := defaultRunnerConfig()
	if err := config.apply(options); err != nil {
		return nil, err
	}
	return config.build(target)
}

type RunnerOption interface {
	applyRunnerOption(*runnerConfig) error
}

type runnerOptionFunc func(*runnerConfig) error

func (option runnerOptionFunc) applyRunnerOption(config *runnerConfig) error {
	return option(config)
}

func WithExecutionStore(store runtime.ExecutionStore) RunnerOption {
	return runnerOptionFunc(func(config *runnerConfig) error {
		if store == nil {
			return fmt.Errorf("execution store is required")
		}
		config.executionStore = store
		return nil
	})
}

func WithCheckpointStore(store runtime.CheckpointStore) RunnerOption {
	return runnerOptionFunc(func(config *runnerConfig) error {
		if store == nil {
			return fmt.Errorf("checkpoint store is required")
		}
		config.checkpointStore = store
		return nil
	})
}

func WithEventSink(sink runtime.EventSink) RunnerOption {
	return runnerOptionFunc(func(config *runnerConfig) error {
		if sink == nil {
			return fmt.Errorf("event sink is required")
		}
		config.eventSink = sink
		return nil
	})
}

func WithArtifactStore(store runtime.ArtifactStore) RunnerOption {
	return runnerOptionFunc(func(config *runnerConfig) error {
		if store == nil {
			return fmt.Errorf("artifact store is required")
		}
		config.artifactStore = store
		return nil
	})
}

func WithStateCodec(codec state.StateCodec) RunnerOption {
	return runnerOptionFunc(func(config *runnerConfig) error {
		if codec == nil {
			return fmt.Errorf("state codec is required")
		}
		config.codec = codec
		return nil
	})
}

func WithGraphID(id string) RunnerOption {
	return runnerOptionFunc(func(config *runnerConfig) error {
		config.graphID = id
		return nil
	})
}

func WithGraphVersion(version string) RunnerOption {
	return runnerOptionFunc(func(config *runnerConfig) error {
		config.graphVersion = version
		return nil
	})
}

func WithBreakpoints(breakpoints ...runtime.Breakpoint) RunnerOption {
	return runnerOptionFunc(func(config *runnerConfig) error {
		config.breakpoints = append([]runtime.Breakpoint(nil), breakpoints...)
		return nil
	})
}

func WithContractValidation(mode core.ContractValidationMode) RunnerOption {
	return runnerOptionFunc(func(config *runnerConfig) error {
		config.contractValidation = mode
		return nil
	})
}

func WithContractPolicy(policy runtime.ContractPolicy) RunnerOption {
	return runnerOptionFunc(func(config *runnerConfig) error {
		config.contractPolicy = policy
		return nil
	})
}

func WithNow(now func() time.Time) RunnerOption {
	return runnerOptionFunc(func(config *runnerConfig) error {
		if now == nil {
			return fmt.Errorf("now function is required")
		}
		config.now = now
		return nil
	})
}

type runnerConfig struct {
	executionStore     runtime.ExecutionStore
	checkpointStore    runtime.CheckpointStore
	eventSink          runtime.EventSink
	artifactStore      runtime.ArtifactStore
	codec              state.StateCodec
	graphID            string
	graphVersion       string
	breakpoints        []runtime.Breakpoint
	contractValidation core.ContractValidationMode
	contractPolicy     runtime.ContractPolicy
	now                func() time.Time
}

func defaultRunnerConfig() runnerConfig {
	executionStore := runtime.NewMemoryExecutionStore()
	checkpointStore := runtime.NewMemoryCheckpointStore()
	eventSink := runtime.NewMemoryEventSink()
	artifactStore := runtime.NewMemoryArtifactStore()
	return runnerConfig{
		executionStore:     executionStore,
		checkpointStore:    checkpointStore,
		eventSink:          eventSink,
		artifactStore:      artifactStore,
		codec:              state.NewJSONStateCodec(""),
		contractValidation: core.ContractValidationStrict,
	}
}

func (config *runnerConfig) apply(options []RunnerOption) error {
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option.applyRunnerOption(config); err != nil {
			return err
		}
	}
	return nil
}

func (config *runnerConfig) build(target *graph.Graph) (*runtime.GraphRunner, error) {
	if target == nil {
		return nil, fmt.Errorf("graph is required")
	}
	options := []runtime.GraphRunnerOption{
		runtime.WithArtifactStore(config.artifactStore),
		runtime.WithRunDeleter(config.runDeleter()),
		runtime.WithGraphMetadata(config.graphID, config.graphVersion, "", "", ""),
		runtime.WithBreakpoints(config.breakpoints...),
		runtime.WithContractValidation(config.contractValidation),
		runtime.WithContractPolicy(config.contractPolicy),
	}
	if config.now != nil {
		options = append(options, runtime.WithNow(config.now))
	}
	return graph.NewGraphRunner(target, config.executionStore, config.checkpointStore, config.codec, config.eventSink, options...)
}

func (config *runnerConfig) runDeleter() runtime.RunDeleter {
	executionStore, ok := config.executionStore.(runtime.RunDeleter)
	if !ok {
		return nil
	}
	checkpointStore, _ := config.checkpointStore.(runtime.RunDeleter)
	eventSink, _ := config.eventSink.(runtime.RunDeleter)
	artifactStore, _ := config.artifactStore.(runtime.RunDeleter)
	return runtime.NewRunDeletionCoordinator(executionStore, checkpointStore, eventSink, artifactStore)
}
