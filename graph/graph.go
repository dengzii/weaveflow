package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/dengzii/weaveflow/builtin"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/internal/graphbuild"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/registry"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"

	langgraph "github.com/smallnest/langgraphgo/graph"
	"go.uber.org/zap"
)

const EndNodeRef = "__end__"

type conditionalEdge struct {
	to        string
	condition registry.EdgeCondition
}

func SetLogger(l *zap.Logger) {
	fruntime.SetLogger(l)
}

// Graph is a thin WeaveFlow wrapper around langgraphgo's typed graph.
// It centralizes project-level conventions such as:
// - registering nodes via Node
// - resolving nodes refs by ID
// - copy-on-write nodes invocation
// - serializable conditional edges
type Graph struct {
	nodes               map[string]node.Node
	nodeSpecs           map[string]dsl.GraphNodeSpec
	nodeContracts       map[string]state.Contract
	stateRegistry       *state.Registry
	initialStatePaths   []string
	contractDiagnostics []ContractDiagnostic
	defaultEdges        map[string][]string
	conditionalEdges    map[string][]conditionalEdge
	edgeSpecs           []dsl.GraphEdgeSpec
	entryPoint          string
	finishPoint         string
	retryPolicy         *langgraph.RetryPolicy
	nodeListeners       map[string][]langgraph.NodeListener[*state.State]
	globalListeners     []langgraph.NodeListener[*state.State]
	tracer              *langgraph.Tracer
}

func NewGraph() *Graph {
	stateRegistry, _ := node.NewDefaultRegistry()
	return &Graph{
		nodes:            map[string]node.Node{},
		nodeSpecs:        map[string]dsl.GraphNodeSpec{},
		defaultEdges:     map[string][]string{},
		conditionalEdges: map[string][]conditionalEdge{},
		nodeListeners:    map[string][]langgraph.NodeListener[*state.State]{},
		stateRegistry:    stateRegistry,
	}
}

func LoadGraphDefinitionFile(path string) (dsl.GraphDefinition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return dsl.GraphDefinition{}, err
	}
	def, err := dsl.DeserializeGraphDefinition(data)
	if err != nil {
		return dsl.GraphDefinition{}, fmt.Errorf("load graph definition from %q: %w", path, err)
	}
	return def, nil
}

func LoadGraphFromFile(buildContext *registry.BuildContext, path string) (*Graph, error) {
	def, err := LoadGraphDefinitionFile(path)
	if err != nil {
		return nil, err
	}
	reg := builtin.NewDefaultRegistry()
	return BuildGraph(reg, def, buildContext)
}

func (g *Graph) EnableLogging() {
	_ = g.AddGlobalListener(NewLoggingListener())
}

func (g *Graph) WriteToFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
	}()
	def, err := g.Definition()
	if err != nil {
		return err
	}
	bytes, err := def.Serialize()
	if err != nil {
		return err
	}
	_, err = f.WriteString(string(bytes))
	return err
}

func (g *Graph) DrawMermaid() (string, error) {
	graph := langgraph.NewStateGraph[*state.State]()
	err := g.buildStateGraph(graph, func(nodeID string, node node.Node) {})
	if err != nil {
		return "", err
	}
	exporter := langgraph.NewExporter(graph)
	return exporter.DrawMermaid(), nil

}

func (g *Graph) AddNode(targetNode node.Node) error {
	if targetNode == nil {
		return fmt.Errorf("nodes is nil")
	}

	id := strings.TrimSpace(targetNode.ID())
	if id == "" {
		setter, ok := targetNode.(interface{ SetID(string) })
		if !ok {
			return fmt.Errorf("nodes id is empty and node does not support automatic id assignment")
		}
		id = g.allocateNodeID(targetNode)
		setter.SetID(id)
	}
	if _, exists := g.nodes[id]; exists {
		return fmt.Errorf("nodes id %q already exists", id)
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
		// this is a nodes that doesn't provide a spec, should we add a default spec? or throw an error?
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
	return nil
}

func (g *Graph) allocateNodeID(targetNode node.Node) string {
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

func graphDefaultNodeID(targetNode node.Node) string {
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

func (g *Graph) SetNodeSpec(spec dsl.GraphNodeSpec) {
	if g == nil {
		return
	}
	id := strings.TrimSpace(spec.ID)
	if id == "" {
		return
	}
	if len(spec.Config) > 0 {
		spec.Config = config.CloneMap(spec.Config)
	}
	g.nodeSpecs[id] = spec
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
	return g.addConditionalEdgeInternal(from, to, condition, true)
}

func (g *Graph) addRuntimeConditionalEdge(from, to string, condition registry.EdgeCondition) error {
	return g.addConditionalEdgeInternal(from, to, condition, false)
}

func (g *Graph) addConditionalEdgeInternal(from, to string, condition registry.EdgeCondition, trackSpec bool) error {
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
	})
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

func (g *Graph) SetRetryPolicy(policy *langgraph.RetryPolicy) {
	g.retryPolicy = policy
}

func (g *Graph) SetTracer(tracer *langgraph.Tracer) {
	g.tracer = tracer
}

func (g *Graph) AddNodeListener(nodeRef string, listener langgraph.NodeListener[*state.State]) error {
	if listener == nil {
		return fmt.Errorf("listener is nil")
	}
	nodeID, err := g.resolveNodeID(nodeRef)
	if err != nil {
		return err
	}
	g.nodeListeners[nodeID] = append(g.nodeListeners[nodeID], listener)
	return nil
}

func (g *Graph) AddGlobalListener(listener langgraph.NodeListener[*state.State]) error {
	if listener == nil {
		return fmt.Errorf("listener is nil")
	}
	g.globalListeners = append(g.globalListeners, listener)
	return nil
}

func (g *Graph) Validate() error {
	if len(g.nodes) == 0 {
		return fmt.Errorf("graph has no nodes")
	}
	if g.entryPoint == "" {
		return fmt.Errorf("entry point is not set")
	}
	if _, ok := g.nodes[g.entryPoint]; !ok {
		return fmt.Errorf("entry point %q not found", g.entryPoint)
	}
	if g.finishPoint != "" {
		if _, ok := g.nodes[g.finishPoint]; !ok {
			return fmt.Errorf("finish point %q not found", g.finishPoint)
		}
	}

	for from, targets := range g.defaultEdges {
		if _, ok := g.nodes[from]; !ok {
			return fmt.Errorf("edge source %q not found", from)
		}
		seenTargets := map[string]struct{}{}
		for _, to := range targets {
			if _, exists := seenTargets[to]; exists {
				return fmt.Errorf("default edge %q -> %q is duplicated", from, g.serializeNodeRef(to))
			}
			seenTargets[to] = struct{}{}
			if to != langgraph.END {
				if _, ok := g.nodes[to]; !ok {
					return fmt.Errorf("edge target %q not found", to)
				}
			}
		}
	}

	for from := range g.conditionalEdges {
		if len(g.defaultEdges[from]) > 1 {
			return fmt.Errorf("nodes %q cannot combine conditional edges with multiple default fallback edges", from)
		}
		if len(g.defaultEdges[from]) == 0 && from != g.finishPoint {
			return fmt.Errorf("nodes %q has conditional edges but no default fallback edge", from)
		}
	}

	for from, edges := range g.conditionalEdges {
		if _, ok := g.nodes[from]; !ok {
			return fmt.Errorf("conditional edge source %q not found", from)
		}
		for _, edge := range edges {
			if err := edge.condition.Validate(); err != nil {
				return fmt.Errorf("conditional edge from %q to %q: %w", from, edge.to, err)
			}
			if edge.to != langgraph.END {
				if _, ok := g.nodes[edge.to]; !ok {
					return fmt.Errorf("conditional edge target %q not found", edge.to)
				}
			}
		}
	}
	if err := g.validateTopology(); err != nil {
		return err
	}

	if len(g.nodeContracts) > 0 {
		g.contractDiagnostics = graphbuild.AnalyzeContractDiagnostics(g.contractAnalysisGraph())
		if err := graphbuild.ContractDiagnosticsError(g.contractDiagnostics); err != nil {
			return err
		}
	} else {
		g.contractDiagnostics = nil
	}

	return nil
}

func (g *Graph) validateTopology() error {
	reachable := g.reachableNodes()
	for _, nodeID := range g.sortedNodeIDs() {
		if _, ok := reachable[nodeID]; !ok {
			return fmt.Errorf("nodes %q is unreachable from entry point %q", nodeID, g.entryPoint)
		}
	}

	if g.finishPoint != "" {
		if len(g.defaultEdges[g.finishPoint]) > 0 || len(g.conditionalEdges[g.finishPoint]) > 0 {
			return fmt.Errorf("finish point %q cannot have outgoing edges", g.finishPoint)
		}
	}

	for _, nodeID := range g.sortedNodeIDs() {
		if _, ok := reachable[nodeID]; !ok {
			continue
		}
		defaultTargets := g.defaultEdges[nodeID]
		conditionalTargets := g.conditionalEdges[nodeID]
		if nodeID == g.finishPoint {
			continue
		}
		if len(defaultTargets) == 0 && len(conditionalTargets) == 0 {
			return fmt.Errorf("nodes %q has no outgoing edge", nodeID)
		}
	}
	terminalReachable := g.terminalReachableNodes()
	for _, nodeID := range g.sortedNodeIDs() {
		if _, ok := reachable[nodeID]; !ok {
			continue
		}
		if _, ok := terminalReachable[nodeID]; !ok {
			return fmt.Errorf("nodes %q cannot reach graph end", nodeID)
		}
	}
	return nil
}

func (g *Graph) sortedNodeIDs() []string {
	if g == nil || len(g.nodes) == 0 {
		return nil
	}
	ids := make([]string, 0, len(g.nodes))
	for nodeID := range g.nodes {
		ids = append(ids, nodeID)
	}
	sort.Strings(ids)
	return ids
}

func (g *Graph) reachableNodes() map[string]struct{} {
	reachable := map[string]struct{}{}
	if g == nil || g.entryPoint == "" {
		return reachable
	}

	queue := []string{g.entryPoint}
	for len(queue) > 0 {
		nodeID := queue[0]
		queue = queue[1:]
		if _, seen := reachable[nodeID]; seen {
			continue
		}
		reachable[nodeID] = struct{}{}

		targets := append([]string(nil), g.defaultEdges[nodeID]...)
		for _, edge := range g.conditionalEdges[nodeID] {
			targets = append(targets, edge.to)
		}
		for _, target := range targets {
			if target == langgraph.END {
				continue
			}
			if _, exists := g.nodes[target]; !exists {
				continue
			}
			if _, seen := reachable[target]; !seen {
				queue = append(queue, target)
			}
		}
	}
	return reachable
}

func (g *Graph) terminalReachableNodes() map[string]struct{} {
	reachable := map[string]struct{}{}
	if g == nil {
		return reachable
	}

	reverseEdges := map[string][]string{}
	queue := []string{}
	addTerminal := func(nodeID string) {
		if nodeID == "" || nodeID == langgraph.END {
			return
		}
		if _, exists := g.nodes[nodeID]; !exists {
			return
		}
		if _, seen := reachable[nodeID]; seen {
			return
		}
		reachable[nodeID] = struct{}{}
		queue = append(queue, nodeID)
	}

	addTerminal(g.finishPoint)
	for from, targets := range g.defaultEdges {
		for _, target := range targets {
			if target == langgraph.END {
				addTerminal(from)
				continue
			}
			reverseEdges[target] = append(reverseEdges[target], from)
		}
	}
	for from, edges := range g.conditionalEdges {
		for _, edge := range edges {
			if edge.to == langgraph.END {
				addTerminal(from)
				continue
			}
			reverseEdges[edge.to] = append(reverseEdges[edge.to], from)
		}
	}

	for len(queue) > 0 {
		nodeID := queue[0]
		queue = queue[1:]
		for _, predecessor := range reverseEdges[nodeID] {
			if _, seen := reachable[predecessor]; seen {
				continue
			}
			reachable[predecessor] = struct{}{}
			queue = append(queue, predecessor)
		}
	}
	return reachable
}

func (g *Graph) Compile() (*Runnable, error) {
	compiled := langgraph.NewListenableStateGraph[*state.State]()
	patches := newCompilePatchCollector(g.compileBranchOrders())
	if err := g.buildStateGraph(compiled.StateGraph, func(nodeID string, node node.Node) {
		nodeDef := node
		listenableNode := compiled.AddNode(nodeID, node.Description(), func(ctx context.Context, state *state.State) (*state.State, error) {
			return g.executePatchNode(ctx, nodeID, nodeDef, state, patches)
		})
		for _, listener := range g.nodeListeners[nodeID] {
			listenableNode.AddListener(g.displayNameListener(listener))
		}
	}); err != nil {
		return nil, err
	}

	for _, listener := range g.globalListeners {
		compiled.AddGlobalListener(g.displayNameListener(listener))
	}
	g.configureStateMerger(compiled.StateGraph, patches)

	runnable, err := compiled.CompileListenable()
	if err != nil {
		return nil, err
	}
	g.applyTracer(runnable)

	return &Runnable{runnable: runnable}, nil
}

func (g *Graph) executePatchNode(ctx context.Context, nodeID string, targetNode node.Node, currentState *state.State, patches *compilePatchCollector) (*state.State, error) {
	if targetNode == nil {
		return currentState, fmt.Errorf("node %q is nil", nodeID)
	}
	result, err := node.Execute(ctx, g.stateAccessorRegistry(), currentState, targetNode)
	if err != nil {
		return currentState, err
	}

	contract := result.Contract
	if g != nil && len(g.nodeContracts) > 0 {
		if nodeContract, ok := g.nodeContracts[nodeID]; ok {
			contract = nodeContract
		}
	}
	if len(contract.Fields) > 0 || contract.WildcardWrite {
		if issues := state.ValidatePatchByContract(result.Patch, contract); len(issues) > 0 {
			return currentState, fmt.Errorf("node %q state contract violation: %s", nodeID, issues[0].Message)
		}
	}
	if patches != nil {
		patches.record(currentState, nodeID, result.Patch)
	}
	return result.State, nil
}

func (g *Graph) compileForRunner(execution fruntime.RunnerExecution) (*langgraph.StateRunnable[*state.State], error) {
	if err := g.Validate(); err != nil {
		return nil, err
	}
	if execution == nil {
		return nil, fmt.Errorf("runner execution is nil")
	}

	compiled := langgraph.NewStateGraph[*state.State]()
	patches := newCompilePatchCollector(g.compileBranchOrders())
	if setter, ok := execution.(fruntime.BranchPatchRecorderSetter); ok {
		setter.SetBranchPatchRecorder(patches)
	}
	if recorder, ok := execution.(fruntime.ParallelWaveRecorder); ok {
		patches.setWaveRecorder(recorder)
	}
	if err := g.configureStateGraph(compiled, func(nodeID string, node node.Node) {
		nodeDef := node
		compiled.AddNode(nodeID, node.Description(), func(ctx context.Context, state *state.State) (*state.State, error) {
			next, err := execution.ExecuteNode(ctx, nodeID, nodeDef, state)
			if err == nil && !patches.hasPatch(state, nodeID) {
				patches.record(state, nodeID, stateDiffPatch(state, next))
			}
			return next, err
		})
	}); err != nil {
		return nil, err
	}
	g.configureStateMerger(compiled, patches)

	runnable, err := compiled.Compile()
	if err != nil {
		return nil, err
	}
	g.applyTracer(runnable)
	return runnable, nil
}

func (g *Graph) buildStateGraph(compiled *langgraph.StateGraph[*state.State], addNode func(nodeID string, node node.Node)) error {
	if err := g.Validate(); err != nil {
		return err
	}
	return g.configureStateGraph(compiled, addNode)
}

func (g *Graph) configureStateGraph(compiled *langgraph.StateGraph[*state.State], addNode func(nodeID string, node node.Node)) error {
	if compiled == nil {
		return fmt.Errorf("compiled graph is nil")
	}
	if addNode == nil {
		return fmt.Errorf("add nodes callback is nil")
	}
	if g.retryPolicy != nil {
		compiled.SetRetryPolicy(g.retryPolicy)
	}

	for nodeID, node := range g.nodes {
		addNode(nodeID, node)
	}

	for from, conditional := range g.conditionalEdges {
		compiled.AddConditionalEdge(from, g.conditionalEdgeResolver(from, conditional))
	}

	for from, targets := range g.defaultEdges {
		if _, hasConditional := g.conditionalEdges[from]; hasConditional {
			continue
		}
		for _, to := range targets {
			compiled.AddEdge(from, to)
		}
	}

	if g.finishPoint != "" {
		if _, hasConditional := g.conditionalEdges[g.finishPoint]; !hasConditional {
			if len(g.defaultEdges[g.finishPoint]) == 0 {
				compiled.AddEdge(g.finishPoint, langgraph.END)
			}
		}
	}

	compiled.SetEntryPoint(g.entryPoint)
	return nil
}

func (g *Graph) configureStateMerger(compiled *langgraph.StateGraph[*state.State], patches *compilePatchCollector) {
	if compiled == nil {
		return
	}
	compiled.SetStateMerger(func(ctx context.Context, current *state.State, newStates []*state.State) (*state.State, error) {
		if len(newStates) == 0 {
			if current == nil {
				return state.NewState(), nil
			}
			return current, nil
		}
		if len(newStates) == 1 {
			if patches != nil {
				_ = patches.consume(current)
			}
			return newStates[0], nil
		}
		if patches == nil {
			return nil, fmt.Errorf("parallel state merge requires branch patches")
		}
		branches := patches.consume(current)
		if len(branches) != len(newStates) {
			return nil, fmt.Errorf("parallel state merge requires branch patches: collected %d for %d branch states", len(branches), len(newStates))
		}
		patches.recordWave(current, branches)
		return state.MergeParallelPatches(current, branches, state.ParallelMergeOptions{
			Contracts: g.nodeContracts,
		})
	})
}

func (g *Graph) compileBranchOrders() map[string]int {
	if g == nil {
		return nil
	}
	orders := map[string]int{}
	nextOrder := 0
	for _, edge := range g.edgeSpecs {
		if edge.Condition != nil {
			continue
		}
		target := strings.TrimSpace(edge.To)
		if target == EndNodeRef {
			target = langgraph.END
		}
		if _, exists := orders[target]; exists {
			continue
		}
		orders[target] = nextOrder
		nextOrder++
	}
	return orders
}

func (g *Graph) isParallelBranchTarget(nodeID string) bool {
	if g == nil || strings.TrimSpace(nodeID) == "" {
		return false
	}
	for from, targets := range g.defaultEdges {
		if len(targets) <= 1 {
			continue
		}
		if len(g.conditionalEdges[from]) > 0 {
			continue
		}
		for _, target := range targets {
			if target == nodeID {
				return true
			}
		}
	}
	return false
}

func stateDiffPatch(before, after *state.State) state.Patch {
	beforeFlat := flattenStateForPatch(before)
	afterFlat := flattenStateForPatch(after)
	paths := make([]string, 0, len(beforeFlat)+len(afterFlat))
	seen := map[string]struct{}{}
	for path := range beforeFlat {
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	for path := range afterFlat {
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	sort.Strings(paths)

	ops := make([]state.PatchOp, 0, len(paths))
	for _, path := range paths {
		beforeValue, beforeOK := beforeFlat[path]
		afterValue, afterOK := afterFlat[path]
		if beforeOK && afterOK && jsonValuesEqual(beforeValue, afterValue) {
			continue
		}
		parsed, err := state.ParsePath(path)
		if err != nil {
			continue
		}
		if !afterOK {
			ops = append(ops, state.PatchOp{Kind: state.OpDelete, Path: parsed})
			continue
		}
		ops = append(ops, state.PatchOp{Kind: state.OpSet, Path: parsed, Value: afterValue})
	}
	return state.NewPatch(ops...)
}

func flattenStateForPatch(current *state.State) map[string]any {
	out := map[string]any{}
	if current == nil {
		return out
	}
	for section, value := range current.Export() {
		flattenStateValueForPatch(out, section, value)
	}
	return out
}

func flattenStateValueForPatch(out map[string]any, path string, value any) {
	mapped, ok := value.(map[string]any)
	if !ok || len(mapped) == 0 {
		out[path] = value
		return
	}
	for key, item := range mapped {
		flattenStateValueForPatch(out, path+"."+key, item)
	}
}

func jsonValuesEqual(left, right any) bool {
	leftBytes, leftErr := json.Marshal(left)
	rightBytes, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftBytes) == string(rightBytes)
}

func (g *Graph) conditionalEdgeResolver(from string, conditional []conditionalEdge) func(ctx context.Context, state *state.State) string {
	edges := append([]conditionalEdge(nil), conditional...)
	defaultTargets := append([]string(nil), g.defaultEdges[from]...)
	isFinishPoint := from == g.finishPoint

	return func(ctx context.Context, state *state.State) string {
		for _, edge := range edges {
			if edge.condition.Match(ctx, state) {
				return edge.to
			}
		}
		if len(defaultTargets) > 0 {
			return defaultTargets[0]
		}
		if isFinishPoint {
			return langgraph.END
		}
		return ""
	}
}

func (g *Graph) applyTracer(target interface{ SetTracer(*langgraph.Tracer) }) {
	if g.tracer != nil {
		target.SetTracer(g.tracer)
	}
}

func (g *Graph) stateAccessorRegistry() *state.Registry {
	if g == nil || g.stateRegistry == nil {
		registry, _ := node.NewDefaultRegistry()
		return registry
	}
	return g.stateRegistry
}

func (g *Graph) Run(ctx context.Context, initialState *state.State) (*state.State, error) {
	runnable, err := g.Compile()
	if err != nil {
		return initialState, err
	}
	return runnable.Invoke(ctx, initialState)
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
		return "", fmt.Errorf("nodes id is empty")
	}
	if _, ok := g.nodes[ref]; ok {
		return ref, nil
	}
	return "", fmt.Errorf("nodes id %q not found", ref)
}

func (g *Graph) resolveEdgeTarget(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("edge target is empty")
	}
	if ref == langgraph.END || ref == EndNodeRef {
		return langgraph.END, nil
	}
	return g.resolveNodeID(ref)
}

func (g *Graph) Definition() (dsl.GraphDefinition, error) {
	if err := g.Validate(); err != nil {
		return dsl.GraphDefinition{}, err
	}

	nodeIDs := make([]string, 0, len(g.nodes))
	for nodeID := range g.nodes {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Slice(nodeIDs, func(i, j int) bool {
		left := g.nodeSpecs[nodeIDs[i]]
		right := g.nodeSpecs[nodeIDs[j]]
		if left.ID == right.ID {
			return left.Name < right.Name
		}
		return left.ID < right.ID
	})

	nodeList := make([]dsl.GraphNodeSpec, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		spec := g.nodeSpecs[nodeID]
		if spec.Type == "" {
			return dsl.GraphDefinition{}, fmt.Errorf("nodes %q is not serializable: missing registered type", nodeID)
		}
		if len(spec.Config) > 0 {
			spec.Config = config.CloneMap(spec.Config)
		}
		nodeList = append(nodeList, spec)
	}

	edges := make([]dsl.GraphEdgeSpec, len(g.edgeSpecs))
	for i, edge := range g.edgeSpecs {
		edges[i] = edge
		if edge.Condition != nil && len(edge.Condition.Config) > 0 {
			copyCondition := *edge.Condition
			copyCondition.Config = config.CloneMap(edge.Condition.Config)
			edges[i].Condition = &copyCondition
		}
	}

	return dsl.GraphDefinition{
		Version:     dsl.GraphDefinitionVersion,
		StateSchema: dsl.CommonStateSchemaID,
		EntryPoint:  g.serializeNodeRef(g.entryPoint),
		FinishPoint: g.serializeNodeRef(g.finishPoint),
		Nodes:       nodeList,
		Edges:       edges,
	}, nil
}

func (g *Graph) serializeNodeRef(nodeID string) string {
	if nodeID == "" {
		return ""
	}
	if nodeID == langgraph.END {
		return EndNodeRef
	}
	return nodeID
}

func (g *Graph) nodeDisplayName(nodeID string) string {
	if nodeID == "" {
		return ""
	}
	if spec, ok := g.nodeSpecs[nodeID]; ok {
		if name := strings.TrimSpace(spec.Name); name != "" {
			return name
		}
		if id := strings.TrimSpace(spec.ID); id != "" {
			return id
		}
	}
	return nodeID
}

func (g *Graph) displayNameListener(listener langgraph.NodeListener[*state.State]) langgraph.NodeListener[*state.State] {
	if listener == nil {
		return nil
	}
	return nodeDisplayListener{
		inner: listener,
		resolve: func(nodeID string) string {
			return g.nodeDisplayName(nodeID)
		},
	}
}

type Runnable struct {
	runnable *langgraph.ListenableRunnable[*state.State]
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
	c.patches[base] = append(c.patches[base], state.BranchPatch{
		NodeID: nodeID,
		Order:  order,
		Patch:  patch,
	})
}

func (c *compilePatchCollector) RecordBranchPatch(base *state.State, nodeID string, patch state.Patch) {
	c.record(base, nodeID, patch)
}

func (c *compilePatchCollector) recordWave(base *state.State, branches []state.BranchPatch) string {
	if c == nil || base == nil || len(branches) <= 1 {
		return ""
	}
	c.mu.Lock()
	recorder := c.wave
	c.mu.Unlock()
	if recorder == nil {
		return ""
	}
	nodeIDs := make([]string, 0, len(branches))
	for _, branch := range branches {
		nodeIDs = append(nodeIDs, branch.NodeID)
	}
	return recorder.RecordParallelWave(base, nodeIDs)
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

type nodeDisplayListener struct {
	inner   langgraph.NodeListener[*state.State]
	resolve func(string) string
}

func (l nodeDisplayListener) OnNodeEvent(ctx context.Context, event langgraph.NodeEvent, nodeID string, state *state.State, err error) {
	if l.inner == nil {
		return
	}
	name := nodeID
	if l.resolve != nil {
		if resolved := strings.TrimSpace(l.resolve(nodeID)); resolved != "" {
			name = resolved
		}
	}
	l.inner.OnNodeEvent(ctx, event, name, state, err)
}

func (r *Runnable) Invoke(ctx context.Context, initialState *state.State) (*state.State, error) {
	return r.runnable.Invoke(ctx, initialState)
}

func (r *Runnable) InvokeWithConfig(ctx context.Context, initialState *state.State, config *langgraph.Config) (*state.State, error) {
	return r.runnable.InvokeWithConfig(ctx, initialState, config)
}

func (r *Runnable) Stream(ctx context.Context, initialState *state.State) <-chan langgraph.StreamEvent[*state.State] {
	return r.runnable.Stream(ctx, initialState)
}

func (r *Runnable) SetTracer(tracer *langgraph.Tracer) {
	r.runnable.SetTracer(tracer)
}

func (r *Runnable) WithTracer(tracer *langgraph.Tracer) *Runnable {
	return &Runnable{runnable: r.runnable.WithTracer(tracer)}
}

func (r *Runnable) GetTracer() *langgraph.Tracer {
	return r.runnable.GetTracer()
}

func (r *Runnable) Underlying() *langgraph.ListenableRunnable[*state.State] {
	return r.runnable
}
