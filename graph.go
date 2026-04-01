package falcon

import (
	"context"
	fruntime "falcon/runtime"
	"fmt"
	"os"
	"sort"
	"strings"

	langgraph "github.com/smallnest/langgraphgo/graph"
)

const EndNodeRef = "__end__"

type conditionalEdge struct {
	to        string
	condition EdgeCondition
}

// Graph is a thin falcon wrapper around langgraphgo's typed graph.
// It centralizes project-level conventions such as:
// - registering nodes via Node
// - resolving node refs by ID
// - copy-on-write node invocation
// - serializable conditional edges
type Graph struct {
	nodes            map[string]Node[State]
	nodeSpecs        map[string]GraphNodeSpec
	edges            map[string]string
	conditionalEdges map[string][]conditionalEdge
	edgeSpecs        []GraphEdgeSpec
	entryPoint       string
	finishPoint      string
	retryPolicy      *langgraph.RetryPolicy
	nodeListeners    map[string][]langgraph.NodeListener[State]
	globalListeners  []langgraph.NodeListener[State]
	tracer           *langgraph.Tracer
}

func NewGraph() *Graph {
	return &Graph{
		nodes:            map[string]Node[State]{},
		nodeSpecs:        map[string]GraphNodeSpec{},
		edges:            map[string]string{},
		conditionalEdges: map[string][]conditionalEdge{},
		nodeListeners:    map[string][]langgraph.NodeListener[State]{},
	}
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
	bytes, err := SerializeGraphDefinition(def)
	if err != nil {
		return err
	}
	_, err = f.WriteString(string(bytes))
	return err
}

func (g *Graph) DrawMermaid() (string, error) {
	graph := langgraph.NewStateGraph[State]()
	err := g.buildStateGraph(graph, func(nodeID string, node Node[State]) {})
	if err != nil {
		return "", err
	}
	exporter := langgraph.NewExporter(graph)
	return exporter.DrawMermaid(), nil

}

func (g *Graph) AddNode(node Node[State]) error {
	if node == nil {
		return fmt.Errorf("node is nil")
	}

	id := strings.TrimSpace(node.ID())
	if id == "" {
		return fmt.Errorf("node id is empty")
	}
	if _, exists := g.nodes[id]; exists {
		return fmt.Errorf("node id %q already exists", id)
	}

	g.nodes[id] = node
	if provider, ok := node.(GraphNodeSpecProvider); ok {
		spec := provider.GraphNodeSpec()
		spec.ID = id
		if spec.Name == "" {
			spec.Name = strings.TrimSpace(node.Name())
		}
		if spec.Description == "" {
			spec.Description = node.Description()
		}
		if spec.Name == "" {
			spec.Name = id
		}
		g.nodeSpecs[id] = spec
	} else {
		// this is a node that doesn't provide a spec, should we add a default spec? or throw an error?
		name := strings.TrimSpace(node.Name())
		if name == "" {
			name = id
		}
		g.nodeSpecs[id] = GraphNodeSpec{
			ID:          id,
			Name:        name,
			Description: node.Description(),
		}
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
	fromID, err := g.resolveNodeID(from)
	if err != nil {
		return err
	}
	toID, err := g.resolveEdgeTarget(to)
	if err != nil {
		return err
	}
	if _, exists := g.edges[fromID]; exists {
		return fmt.Errorf("node %q already has a default edge", fromID)
	}
	g.edges[fromID] = toID
	g.edgeSpecs = append(g.edgeSpecs, GraphEdgeSpec{
		From: g.nodeSpecs[fromID].ID,
		To:   g.serializeNodeRef(toID),
	})
	return nil
}

func (g *Graph) AddConditionalEdge(from, to string, condition EdgeCondition) error {
	if err := condition.validate(); err != nil {
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
	spec := condition.cloneSpec()
	g.edgeSpecs = append(g.edgeSpecs, GraphEdgeSpec{
		From:      g.nodeSpecs[fromID].ID,
		To:        g.serializeNodeRef(toID),
		Condition: &spec,
	})
	return nil
}

func (g *Graph) SetRetryPolicy(policy *langgraph.RetryPolicy) {
	g.retryPolicy = policy
}

func (g *Graph) SetTracer(tracer *langgraph.Tracer) {
	g.tracer = tracer
}

func (g *Graph) AddNodeListener(nodeRef string, listener langgraph.NodeListener[State]) error {
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

func (g *Graph) AddGlobalListener(listener langgraph.NodeListener[State]) error {
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

	for from, to := range g.edges {
		if _, ok := g.nodes[from]; !ok {
			return fmt.Errorf("edge source %q not found", from)
		}
		if to != langgraph.END {
			if _, ok := g.nodes[to]; !ok {
				return fmt.Errorf("edge target %q not found", to)
			}
		}
	}

	for from, edges := range g.conditionalEdges {
		if _, ok := g.nodes[from]; !ok {
			return fmt.Errorf("conditional edge source %q not found", from)
		}
		for _, edge := range edges {
			if err := edge.condition.validate(); err != nil {
				return fmt.Errorf("conditional edge from %q to %q: %w", from, edge.to, err)
			}
			if edge.to != langgraph.END {
				if _, ok := g.nodes[edge.to]; !ok {
					return fmt.Errorf("conditional edge target %q not found", edge.to)
				}
			}
		}
	}

	return nil
}

func (g *Graph) Compile() (*Runnable, error) {
	compiled := langgraph.NewListenableStateGraph[State]()
	if err := g.buildStateGraph(compiled.StateGraph, func(nodeID string, node Node[State]) {
		nodeDef := node
		listenableNode := compiled.AddNode(nodeID, node.Description(), func(ctx context.Context, state State) (State, error) {
			return nodeDef.Invoke(ctx, state.CloneState())
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

	runnable, err := compiled.CompileListenable()
	if err != nil {
		return nil, err
	}
	g.applyTracer(runnable)

	return &Runnable{runnable: runnable}, nil
}

func (g *Graph) compileForRunner(execution fruntime.RunnerExecution) (*langgraph.StateRunnable[State], error) {
	if err := g.Validate(); err != nil {
		return nil, err
	}
	if execution == nil {
		return nil, fmt.Errorf("runner execution is nil")
	}

	compiled := langgraph.NewStateGraph[State]()
	if err := g.configureStateGraph(compiled, func(nodeID string, node Node[State]) {
		nodeDef := node
		compiled.AddNode(nodeID, node.Description(), func(ctx context.Context, state State) (State, error) {
			return execution.InvokeNode(ctx, nodeID,
				func(ctx context.Context, state State) (State, error) {
					return nodeDef.Invoke(ctx, state)
				}, state,
			)
		})
	}); err != nil {
		return nil, err
	}

	runnable, err := compiled.Compile()
	if err != nil {
		return nil, err
	}
	g.applyTracer(runnable)
	return runnable, nil
}

func (g *Graph) buildStateGraph(compiled *langgraph.StateGraph[State], addNode func(nodeID string, node Node[State])) error {
	if err := g.Validate(); err != nil {
		return err
	}
	return g.configureStateGraph(compiled, addNode)
}

func (g *Graph) configureStateGraph(compiled *langgraph.StateGraph[State], addNode func(nodeID string, node Node[State])) error {
	if compiled == nil {
		return fmt.Errorf("compiled graph is nil")
	}
	if addNode == nil {
		return fmt.Errorf("add node callback is nil")
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

	for from, to := range g.edges {
		if _, hasConditional := g.conditionalEdges[from]; hasConditional {
			continue
		}
		compiled.AddEdge(from, to)
	}

	if g.finishPoint != "" {
		if _, hasConditional := g.conditionalEdges[g.finishPoint]; !hasConditional {
			if _, hasDefaultEdge := g.edges[g.finishPoint]; !hasDefaultEdge {
				compiled.AddEdge(g.finishPoint, langgraph.END)
			}
		}
	}

	compiled.SetEntryPoint(g.entryPoint)
	return nil
}

func (g *Graph) conditionalEdgeResolver(from string, conditional []conditionalEdge) func(ctx context.Context, state State) string {
	edges := append([]conditionalEdge(nil), conditional...)
	defaultTarget, hasDefaultTarget := g.edges[from]
	isFinishPoint := from == g.finishPoint

	return func(ctx context.Context, state State) string {
		for _, edge := range edges {
			if edge.condition.Match(ctx, state) {
				return edge.to
			}
		}
		if hasDefaultTarget {
			return defaultTarget
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

func (g *Graph) Run(ctx context.Context, initialState State) (State, error) {
	runnable, err := g.Compile()
	if err != nil {
		return initialState, err
	}
	return runnable.Invoke(ctx, initialState)
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
	if ref == langgraph.END || ref == EndNodeRef {
		return langgraph.END, nil
	}
	return g.resolveNodeID(ref)
}

func (g *Graph) Definition() (GraphDefinition, error) {
	if err := g.Validate(); err != nil {
		return GraphDefinition{}, err
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

	nodes := make([]GraphNodeSpec, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		spec := g.nodeSpecs[nodeID]
		if spec.Type == "" {
			return GraphDefinition{}, fmt.Errorf("node %q is not serializable: missing registered type", nodeID)
		}
		if len(spec.Config) > 0 {
			spec.Config = cloneMap(spec.Config)
		}
		nodes = append(nodes, spec)
	}

	edges := make([]GraphEdgeSpec, len(g.edgeSpecs))
	for i, edge := range g.edgeSpecs {
		edges[i] = edge
		if edge.Condition != nil && len(edge.Condition.Config) > 0 {
			copyCondition := *edge.Condition
			copyCondition.Config = cloneMap(edge.Condition.Config)
			edges[i].Condition = &copyCondition
		}
	}

	return GraphDefinition{
		Version:     GraphDefinitionVersion,
		StateSchema: fruntime.CommonStateSchemaID,
		EntryPoint:  g.serializeNodeRef(g.entryPoint),
		FinishPoint: g.serializeNodeRef(g.finishPoint),
		Nodes:       nodes,
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

func (g *Graph) displayNameListener(listener langgraph.NodeListener[State]) langgraph.NodeListener[State] {
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

func cloneMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

type Runnable struct {
	runnable *langgraph.ListenableRunnable[State]
}

type nodeDisplayListener struct {
	inner   langgraph.NodeListener[State]
	resolve func(string) string
}

func (l nodeDisplayListener) OnNodeEvent(ctx context.Context, event langgraph.NodeEvent, nodeID string, state State, err error) {
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

func (r *Runnable) Invoke(ctx context.Context, initialState State) (State, error) {
	return r.runnable.Invoke(ctx, initialState)
}

func (r *Runnable) InvokeWithConfig(ctx context.Context, initialState State, config *langgraph.Config) (State, error) {
	return r.runnable.InvokeWithConfig(ctx, initialState, config)
}

func (r *Runnable) Stream(ctx context.Context, initialState State) <-chan langgraph.StreamEvent[State] {
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

func (r *Runnable) Underlying() *langgraph.ListenableRunnable[State] {
	return r.runnable
}
