// Package graph builds, validates, compiles, and executes graph topology.
package graph

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dengzii/weaveflow/builtin"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/internal/graphbuild"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/registry"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"

	"go.uber.org/zap"
)

const (
	EndNodeRef = dsl.EndNodeRef
	endNodeID  = "END"
)

type BackoffStrategy string

const (
	FixedBackoff       BackoffStrategy = "fixed"
	LinearBackoff      BackoffStrategy = "linear"
	ExponentialBackoff BackoffStrategy = "exponential"
)

type RetryPolicy struct {
	MaxRetries      int
	BackoffStrategy BackoffStrategy
	RetryableErrors []string
}

type NodeEvent string

const (
	EventChainStart   NodeEvent = "chain_start"
	EventChainEnd     NodeEvent = "chain_end"
	EventNodeStart    NodeEvent = "start"
	EventNodeComplete NodeEvent = "complete"
	EventNodeError    NodeEvent = "error"
)

type StreamEvent struct {
	Timestamp time.Time
	NodeName  string
	Event     NodeEvent
	State     *state.State
	Error     error
	Duration  time.Duration
}

type conditionalEdge struct {
	to        string
	condition registry.EdgeCondition
	contract  state.Contract
	resolved  bool
}

func SetLogger(l *zap.Logger) {
	fruntime.SetLogger(l)
}

// Graph owns WeaveFlow topology, state contracts, and scheduler configuration.
// It centralizes project-level conventions such as:
// - registering nodes via Node
// - resolving nodes refs by ID
// - copy-on-write nodes invocation
// - serializable conditional edges
type Graph struct {
	registry              *registry.Registry
	nodes                 map[string]core.Node
	nodeSpecs             map[string]dsl.GraphNodeSpec
	nodeContracts         map[string]state.Contract
	conditionContracts    map[string]state.Contract
	stateBindingSemantics []dsl.StateBindingSemantic
	initialStatePaths     []string
	contractDiagnostics   []core.ContractDiagnostic
	defaultEdges          map[string][]string
	conditionalEdges      map[string][]conditionalEdge
	edgeSpecs             []dsl.GraphEdgeSpec
	version               string
	name                  string
	description           string
	stateModules          []dsl.StateModuleRef
	metadata              map[string]any
	entryPoint            string
	finishPoint           string
	retryPolicy           *RetryPolicy
	contractDiagnosticsMu sync.RWMutex
}

func NewGraph(reg *registry.Registry) *Graph {
	protocolModule := builtin.ProtocolsStateModuleDefinition()
	initialStatePaths := make([]string, 0, len(protocolModule.Fields))
	for _, field := range protocolModule.Fields {
		initialStatePaths = append(initialStatePaths, field.Path)
	}
	return &Graph{
		registry:          reg,
		nodes:             map[string]core.Node{},
		nodeSpecs:         map[string]dsl.GraphNodeSpec{},
		defaultEdges:      map[string][]string{},
		conditionalEdges:  map[string][]conditionalEdge{},
		stateModules:      []dsl.StateModuleRef{{Name: builtin.ProtocolsModuleName, Version: builtin.ProtocolsModuleVersion}},
		initialStatePaths: initialStatePaths,
	}
}

func (g *Graph) AddNode(targetNode core.Node) error {
	if targetNode == nil {
		return fmt.Errorf("node is nil")
	}

	id := strings.TrimSpace(targetNode.ID())
	if id == "" {
		setter, ok := targetNode.(interface{ SetID(string) })
		if !ok {
			return fmt.Errorf("node id is empty and node does not support automatic id assignment")
		}
		id = g.allocateNodeID(targetNode)
		setter.SetID(id)
		node.ApplyDefaultStatePaths(targetNode)
	}
	if _, exists := g.nodes[id]; exists {
		return fmt.Errorf("node id %q already exists", id)
	}

	g.nodes[id] = targetNode
	if provider, ok := targetNode.(dsl.GraphNodeSpecProvider); ok {
		spec := provider.GraphNodeSpec()
		spec.ID = id
		if spec.Name == "" {
			spec.Name = strings.TrimSpace(targetNode.Name())
		}
		if spec.Description == "" {
			spec.Description = targetNode.Description()
		}
		if spec.Name == "" {
			spec.Name = id
		}
		g.nodeSpecs[id] = spec
	} else {
		name := strings.TrimSpace(targetNode.Name())
		if name == "" {
			name = id
		}
		g.nodeSpecs[id] = dsl.GraphNodeSpec{
			ID:          id,
			Name:        name,
			Description: targetNode.Description(),
		}
	}
	if err := g.attachNodeContract(id, targetNode); err != nil {
		delete(g.nodes, id)
		delete(g.nodeSpecs, id)
		return err
	}
	return nil
}

func (g *Graph) attachNodeContract(nodeID string, targetNode core.Node) error {
	if g == nil {
		return fmt.Errorf("graph is nil")
	}
	if _, exists := g.nodeContracts[nodeID]; exists {
		return nil
	}
	spec := g.nodeSpecs[nodeID]
	reg := g.registry
	if reg != nil {
		if _, registered := reg.FindNodeType(spec.Type); registered {
			def := dsl.GraphDefinition{
				Version:      dsl.GraphDefinitionVersion,
				StateModules: append([]dsl.StateModuleRef(nil), g.stateModules...),
				Nodes:        []dsl.GraphNodeSpec{spec},
			}
			resolved, err := graphbuild.ResolveGraphBindings(def, reg)
			if err != nil {
				return err
			}
			contract, ok := resolved.NodeContracts[nodeID]
			if !ok {
				return fmt.Errorf("node %q has no resolved state contract", nodeID)
			}
			if g.nodeContracts == nil {
				g.nodeContracts = map[string]state.Contract{}
			}
			g.nodeContracts[nodeID] = contract.Clone()
			return nil
		}
	}

	contract, err := core.ContractFor(targetNode)
	if err != nil {
		return err
	}
	if len(contract.Fields) == 0 && !contract.WildcardRead && !contract.WildcardWrite {
		return nil
	}
	if g.nodeContracts == nil {
		g.nodeContracts = map[string]state.Contract{}
	}
	g.nodeContracts[nodeID] = contract.Clone()
	return nil
}

func (g *Graph) allocateNodeID(targetNode core.Node) string {
	base := graphDefaultNodeID(targetNode)
	if _, exists := g.nodes[base]; !exists {
		return base
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s_%d", base, suffix)
		if _, exists := g.nodes[candidate]; !exists {
			return candidate
		}
	}
}

func graphDefaultNodeID(targetNode core.Node) string {
	if targetNode == nil {
		return "node"
	}
	if provider, ok := targetNode.(dsl.GraphNodeSpecProvider); ok {
		if nodeType := strings.TrimSpace(provider.GraphNodeSpec().Type); nodeType != "" {
			return nodeType
		}
	}
	if name := strings.TrimSpace(targetNode.Name()); name != "" {
		return name
	}
	return "node"
}

func (g *Graph) SetNodeSpec(spec dsl.GraphNodeSpec) error {
	if g == nil {
		return fmt.Errorf("graph is nil")
	}
	id := strings.TrimSpace(spec.ID)
	if id == "" {
		return fmt.Errorf("node spec id is required")
	}
	targetNode, ok := g.nodes[id]
	if !ok {
		return fmt.Errorf("node %q not found", id)
	}
	if len(spec.Config) > 0 {
		spec.Config = config.CloneMap(spec.Config)
	}
	if g.nodeSpecs == nil {
		g.nodeSpecs = map[string]dsl.GraphNodeSpec{}
	}
	previousSpec, hadPreviousSpec := g.nodeSpecs[id]
	previousContract, hadPreviousContract := g.nodeContracts[id]
	g.nodeSpecs[id] = spec
	delete(g.nodeContracts, id)
	if err := g.attachNodeContract(id, targetNode); err != nil {
		if hadPreviousSpec {
			g.nodeSpecs[id] = previousSpec
		} else {
			delete(g.nodeSpecs, id)
		}
		if hadPreviousContract {
			g.nodeContracts[id] = previousContract
		} else {
			delete(g.nodeContracts, id)
		}
		return fmt.Errorf("node %q contract resolution failed: %w", id, err)
	}
	return nil
}

func (g *Graph) SetEntryPoint(ref string) error {
	nodeID, err := g.resolveNodeID(ref)
	if err != nil {
		return err
	}
	g.entryPoint = nodeID
	return nil
}

func (g *Graph) SetFinishPoint(ref string) error {
	nodeID, err := g.resolveNodeID(ref)
	if err != nil {
		return err
	}
	g.finishPoint = nodeID
	return nil
}

func (g *Graph) AddEdge(from, to string) error {
	return g.addEdgeInternal(from, to, true)
}

func (g *Graph) addRuntimeEdge(from, to string) error {
	return g.addEdgeInternal(from, to, false)
}

func (g *Graph) addEdgeInternal(from, to string, trackSpec bool) error {
	fromID, err := g.resolveNodeID(from)
	if err != nil {
		return err
	}
	toID, err := g.resolveEdgeTarget(to)
	if err != nil {
		return err
	}
	for _, target := range g.defaultEdges[fromID] {
		if target == toID {
			return fmt.Errorf("default edge %q -> %q already exists", fromID, g.serializeNodeRef(toID))
		}
	}
	g.defaultEdges[fromID] = append(g.defaultEdges[fromID], toID)
	if trackSpec {
		g.edgeSpecs = append(g.edgeSpecs, dsl.GraphEdgeSpec{
			From: g.nodeSpecs[fromID].ID,
			To:   g.serializeNodeRef(toID),
		})
	}
	return nil
}

func (g *Graph) AddConditionalEdge(from, to string, condition registry.EdgeCondition) error {
	contract, resolved, err := g.resolveDefaultConditionContract(condition.CloneSpec())
	if err != nil {
		return err
	}
	return g.addConditionalEdgeInternal(from, to, condition, true, contract, resolved, true)
}

func (g *Graph) AddResolvedConditionalEdge(from, to string, condition registry.EdgeCondition, contract state.Contract) error {
	return g.addConditionalEdgeInternal(from, to, condition, true, contract, true, false)
}

func (g *Graph) addRuntimeConditionalEdge(from, to string, condition registry.EdgeCondition) error {
	return g.addConditionalEdgeInternal(from, to, condition, false, state.Contract{}, false, false)
}

func (g *Graph) addConditionalEdgeInternal(
	from, to string,
	condition registry.EdgeCondition,
	trackSpec bool,
	contract state.Contract,
	resolved bool,
	trackContract bool,
) error {
	if err := condition.Validate(); err != nil {
		return err
	}

	fromID, err := g.resolveNodeID(from)
	if err != nil {
		return err
	}
	toID, err := g.resolveEdgeTarget(to)
	if err != nil {
		return err
	}

	g.conditionalEdges[fromID] = append(g.conditionalEdges[fromID], conditionalEdge{
		to:        toID,
		condition: condition,
		contract:  contract.Clone(),
		resolved:  resolved,
	})
	if resolved && trackContract {
		g.appendConditionContract(fromID, contract)
	}
	if trackSpec {
		spec := condition.CloneSpec()
		g.edgeSpecs = append(g.edgeSpecs, dsl.GraphEdgeSpec{
			From:      g.nodeSpecs[fromID].ID,
			To:        g.serializeNodeRef(toID),
			Condition: &spec,
		})
	}
	return nil
}

func (g *Graph) resolveDefaultConditionContract(spec dsl.GraphConditionSpec) (state.Contract, bool, error) {
	if g == nil || strings.TrimSpace(spec.Type) == "" {
		return state.Contract{}, false, nil
	}
	reg := g.registry
	if reg == nil {
		return state.Contract{}, false, nil
	}
	if _, ok := reg.FindCondition(spec.Type); !ok {
		return state.Contract{}, false, nil
	}
	def := dsl.GraphDefinition{
		Version:      dsl.GraphDefinitionVersion,
		StateModules: append([]dsl.StateModuleRef(nil), g.stateModules...),
		Edges: []dsl.GraphEdgeSpec{{
			From: "source", To: "target", Condition: &spec,
		}},
	}
	resolved, err := graphbuild.ResolveGraphBindings(def, reg)
	if err != nil {
		return state.Contract{}, false, err
	}
	contract, ok := resolved.ConditionContracts[0]
	if !ok {
		return state.Contract{}, false, fmt.Errorf("condition %q has no resolved state contract", spec.Type)
	}
	return contract, true, nil
}

func (g *Graph) appendConditionContract(from string, contract state.Contract) {
	if g == nil {
		return
	}
	if g.conditionContracts == nil {
		g.conditionContracts = map[string]state.Contract{}
	}
	combined := g.conditionContracts[from].Clone()
	combined.Fields = append(combined.Fields, contract.Fields...)
	combined.WildcardRead = combined.WildcardRead || contract.WildcardRead
	combined.WildcardWrite = combined.WildcardWrite || contract.WildcardWrite
	g.conditionContracts[from] = combined
}

func (g *Graph) SetRetryPolicy(policy *RetryPolicy) {
	if g == nil {
		return
	}
	if policy == nil {
		g.retryPolicy = nil
		return
	}
	copyPolicy := *policy
	copyPolicy.RetryableErrors = append([]string(nil), policy.RetryableErrors...)
	g.retryPolicy = &copyPolicy
}

func (g *Graph) setInitialStatePaths(paths []string) {
	if g == nil {
		return
	}
	if len(paths) == 0 {
		g.initialStatePaths = nil
		return
	}
	g.initialStatePaths = append([]string(nil), paths...)
}

func (g *Graph) setNodeContracts(contracts map[string]state.Contract) {
	if g == nil {
		return
	}
	if len(contracts) == 0 {
		g.nodeContracts = nil
		return
	}
	g.nodeContracts = make(map[string]state.Contract, len(contracts))
	for key, value := range contracts {
		g.nodeContracts[key] = value.Clone()
	}
}

func (g *Graph) setConditionContracts(contracts map[string]state.Contract) {
	if g == nil || len(contracts) == 0 {
		if g != nil {
			g.conditionContracts = nil
		}
		return
	}
	g.conditionContracts = make(map[string]state.Contract, len(contracts))
	for key, value := range contracts {
		g.conditionContracts[key] = value.Clone()
	}
}

func (g *Graph) NodeSpecs() map[string]dsl.GraphNodeSpec {
	if g == nil || len(g.nodeSpecs) == 0 {
		return nil
	}
	cloned := make(map[string]dsl.GraphNodeSpec, len(g.nodeSpecs))
	for key, value := range g.nodeSpecs {
		cloned[key] = value
	}
	return cloned
}

func (g *Graph) resolveNodeID(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("node id is empty")
	}
	if _, ok := g.nodes[ref]; ok {
		return ref, nil
	}
	return "", fmt.Errorf("node id %q not found", ref)
}

func (g *Graph) resolveEdgeTarget(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("edge target is empty")
	}
	if ref == EndNodeRef {
		return endNodeID, nil
	}
	return g.resolveNodeID(ref)
}

type Runnable struct {
	scheduled *scheduledRunnable
}

type compilePatchCollector struct {
	mu      sync.Mutex
	orders  map[string]int
	patches map[*state.State][]state.BranchPatch
	wave    fruntime.ParallelWaveRecorder
}

func newCompilePatchCollector(orders map[string]int) *compilePatchCollector {
	if orders == nil {
		orders = map[string]int{}
	}
	return &compilePatchCollector{
		orders:  orders,
		patches: map[*state.State][]state.BranchPatch{},
	}
}

func (c *compilePatchCollector) record(base *state.State, nodeID string, patch state.Patch) {
	if c == nil || base == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	order, ok := c.orders[nodeID]
	if !ok {
		order = len(c.orders)
		c.orders[nodeID] = order
	}
	branch := state.BranchPatch{
		NodeID: nodeID,
		Order:  order,
		Patch:  patch,
	}
	for i := range c.patches[base] {
		if c.patches[base][i].NodeID == nodeID {
			c.patches[base][i] = branch
			return
		}
	}
	c.patches[base] = append(c.patches[base], branch)
}

func (c *compilePatchCollector) RecordBranchPatch(base *state.State, nodeID string, patch state.Patch) {
	c.record(base, nodeID, patch)
}

func recordFailedBranchPatch(c *compilePatchCollector, base *state.State, nodeID string, err error) {
	if c == nil || base == nil || err == nil {
		return
	}
	var nodeInterrupt *core.NodeInterrupt
	var graphInterrupt *fruntime.GraphInterrupt
	if (errors.As(err, &nodeInterrupt) || errors.As(err, &graphInterrupt)) && c.hasPatch(base, nodeID) {
		return
	}
	c.record(base, nodeID, state.Patch{})
}

func (c *compilePatchCollector) notifyParallelWave(ctx context.Context, base *state.State, branches []state.BranchPatch) error {
	if c == nil || base == nil || len(branches) <= 1 {
		return nil
	}
	c.mu.Lock()
	recorder := c.wave
	c.mu.Unlock()
	if recorder == nil {
		return nil
	}
	nodeIDs := make([]string, 0, len(branches))
	for _, branch := range branches {
		nodeIDs = append(nodeIDs, branch.NodeID)
	}
	return recorder.OnParallelWave(ctx, base, nodeIDs)
}

func (c *compilePatchCollector) setWaveRecorder(recorder fruntime.ParallelWaveRecorder) {
	if c == nil || recorder == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wave = recorder
}

func (c *compilePatchCollector) hasPatch(base *state.State, nodeID string) bool {
	if c == nil || base == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, branch := range c.patches[base] {
		if branch.NodeID == nodeID {
			return true
		}
	}
	return false
}

func (c *compilePatchCollector) consume(base *state.State) []state.BranchPatch {
	if c == nil || base == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	branches := append([]state.BranchPatch(nil), c.patches[base]...)
	delete(c.patches, base)
	return branches
}

func (r *Runnable) Invoke(ctx context.Context, initialState *state.State) (*state.State, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if r == nil {
		return initialState, fmt.Errorf("runnable is nil")
	}
	if r.scheduled == nil {
		return initialState, fmt.Errorf("runnable is not initialized")
	}
	return r.scheduled.Invoke(ctx, initialState)
}

func (r *Runnable) InvokeWithConfig(ctx context.Context, initialState *state.State, config fruntime.SchedulerConfig) (*state.State, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if r == nil {
		return initialState, fmt.Errorf("runnable is nil")
	}
	if r.scheduled == nil {
		return initialState, fmt.Errorf("runnable is not initialized")
	}
	return r.scheduled.InvokeWithConfig(ctx, initialState, config)
}

func (r *Runnable) Stream(ctx context.Context, initialState *state.State) <-chan StreamEvent {
	if ctx == nil {
		ctx = context.Background()
	}
	if r == nil {
		events := make(chan StreamEvent, 1)
		events <- StreamEvent{State: initialState, Error: fmt.Errorf("runnable is nil")}
		close(events)
		return events
	}
	if r.scheduled == nil {
		events := make(chan StreamEvent, 1)
		events <- StreamEvent{State: initialState, Error: fmt.Errorf("runnable is not initialized")}
		close(events)
		return events
	}
	events := make(chan StreamEvent, 100)
	go func() {
		defer close(events)
		send := func(event StreamEvent) bool {
			select {
			case events <- event:
				return true
			case <-ctx.Done():
				return false
			}
		}
		if !send(StreamEvent{Timestamp: time.Now(), Event: EventChainStart, State: initialState}) {
			return
		}
		scheduled := *r.scheduled
		scheduled.observeNode = func(_ context.Context, event NodeEvent, nodeID string, currentState *state.State, err error, duration time.Duration) {
			send(StreamEvent{
				Timestamp: time.Now(),
				NodeName:  nodeID,
				Event:     event,
				State:     currentState,
				Error:     err,
				Duration:  duration,
			})
		}
		finalState, err := scheduled.Invoke(ctx, initialState)
		send(StreamEvent{Timestamp: time.Now(), Event: EventChainEnd, State: finalState, Error: err})
	}()
	return events
}
