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

type EdgeConditionMatcher func(ctx context.Context, state State) bool

type EdgeCondition struct {
	Spec  GraphConditionSpec
	Match EdgeConditionMatcher
}

func NewEdgeCondition(spec GraphConditionSpec, match EdgeConditionMatcher) EdgeCondition {
	return EdgeCondition{
		Spec:  normalizeGraphConditionSpec(spec),
		Match: match,
	}
}

func (c EdgeCondition) validate() error {
	spec := normalizeGraphConditionSpec(c.Spec)
	if spec.Type == "" {
		return fmt.Errorf("condition spec type is required")
	}
	if c.Match == nil {
		return fmt.Errorf("condition matcher is nil")
	}
	return nil
}

func (c EdgeCondition) withSpec(spec GraphConditionSpec) EdgeCondition {
	c.Spec = normalizeGraphConditionSpec(spec)
	return c
}

func (c EdgeCondition) cloneSpec() GraphConditionSpec {
	spec := normalizeGraphConditionSpec(c.Spec)
	if len(spec.Config) > 0 {
		spec.Config = cloneMap(spec.Config)
	}
	return spec
}

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
	defer f.Close()
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
	return g.addEdge(from, to, true)
}

func (g *Graph) addEdge(from, to string, serialize bool) error {
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
	if serialize {
		g.edgeSpecs = append(g.edgeSpecs, GraphEdgeSpec{
			From: g.nodeSpecs[fromID].ID,
			To:   g.serializeNodeRef(toID),
		})
	}
	return nil
}

func (g *Graph) AddConditionalEdge(from, to string, condition EdgeCondition) error {
	return g.addConditionalEdge(from, to, condition, true)
}

func (g *Graph) addConditionalEdge(from, to string, condition EdgeCondition, serialize bool) error {
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
	if serialize {
		spec := condition.cloneSpec()
		g.edgeSpecs = append(g.edgeSpecs, GraphEdgeSpec{
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
	if err := g.Validate(); err != nil {
		return nil, err
	}

	compiled := langgraph.NewListenableStateGraph[State]()
	if g.retryPolicy != nil {
		compiled.SetRetryPolicy(g.retryPolicy)
	}

	for nodeID, node := range g.nodes {
		nodeID := nodeID
		nodeDef := node
		listenableNode := compiled.AddNode(nodeID, node.Description(), func(ctx context.Context, state State) (State, error) {
			return nodeDef.Invoke(ctx, state.CloneState())
		})
		for _, listener := range g.nodeListeners[nodeID] {
			listenableNode.AddListener(g.displayNameListener(listener))
		}
	}

	for _, listener := range g.globalListeners {
		compiled.AddGlobalListener(g.displayNameListener(listener))
	}

	for from, conditional := range g.conditionalEdges {
		edges := append([]conditionalEdge(nil), conditional...)
		defaultTarget, hasDefaultTarget := g.edges[from]
		isFinishPoint := from == g.finishPoint

		compiled.AddConditionalEdge(from, func(ctx context.Context, state State) string {
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
		})
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

	runnable, err := compiled.CompileListenable()
	if err != nil {
		return nil, err
	}
	if g.tracer != nil {
		runnable.SetTracer(g.tracer)
	}

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
	if g.retryPolicy != nil {
		compiled.SetRetryPolicy(g.retryPolicy)
	}

	for nodeID, node := range g.nodes {
		nodeID := nodeID
		nodeDef := node
		compiled.AddNode(nodeID, node.Description(), func(ctx context.Context, state State) (State, error) {
			return execution.InvokeNode(ctx, nodeID,
				func(ctx context.Context, state State) (State, error) {
					return nodeDef.Invoke(ctx, state)
				}, state,
			)
		})
	}

	return g.compileRunnable(compiled)
}

func (g *Graph) compileRunnable(compiled *langgraph.StateGraph[State]) (*langgraph.StateRunnable[State], error) {
	for from, conditional := range g.conditionalEdges {
		edges := append([]conditionalEdge(nil), conditional...)
		defaultTarget, hasDefaultTarget := g.edges[from]
		isFinishPoint := from == g.finishPoint

		compiled.AddConditionalEdge(from, func(ctx context.Context, state State) string {
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
		})
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

	runnable, err := compiled.Compile()
	if err != nil {
		return nil, err
	}
	if g.tracer != nil {
		runnable.SetTracer(g.tracer)
	}
	return runnable, nil
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
