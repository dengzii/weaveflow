package falcon

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	langgraph "github.com/smallnest/langgraphgo/graph"
)

const EndNodeRef = "__end__"

type EdgeCondition func(ctx context.Context, state State) bool

type conditionalEdge struct {
	to        string
	when      EdgeCondition
	condition *GraphConditionSpec
}

// Graph is a thin falcon wrapper around langgraphgo's typed graph.
// It centralizes project-level conventions such as:
// - registering nodes via Node
// - resolving node refs by name or ID
// - copy-on-write node invocation
// - serializable conditional edges
type Graph struct {
	nodes            map[string]Node[State]
	nodeSpecs        map[string]GraphNodeSpec
	nodeIDs          map[string]string
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
		nodeIDs:          map[string]string{},
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

	name := strings.TrimSpace(node.Name())
	if name == "" {
		return fmt.Errorf("node name is empty")
	}
	if _, exists := g.nodes[name]; exists {
		return fmt.Errorf("node name %q already exists", name)
	}

	id := strings.TrimSpace(node.ID())
	if id == "" {
		id = name
	}
	if existing, exists := g.nodeIDs[id]; exists {
		return fmt.Errorf("node id %q already exists for %q", id, existing)
	}

	g.nodes[name] = node
	g.nodeIDs[id] = name
	if provider, ok := node.(GraphNodeSpecProvider); ok {
		spec := provider.GraphNodeSpec()
		spec.ID = id
		if spec.Name == "" {
			spec.Name = name
		}
		if spec.Description == "" {
			spec.Description = node.Description()
		}
		g.nodeSpecs[name] = spec
	} else {
		g.nodeSpecs[name] = GraphNodeSpec{
			ID:          id,
			Name:        name,
			Description: node.Description(),
		}
	}
	return nil
}

func (g *Graph) SetEntryPoint(ref string) error {
	name, err := g.resolveNodeRef(ref)
	if err != nil {
		return err
	}
	g.entryPoint = name
	return nil
}

func (g *Graph) SetFinishPoint(ref string) error {
	name, err := g.resolveNodeRef(ref)
	if err != nil {
		return err
	}
	g.finishPoint = name
	return nil
}

func (g *Graph) AddEdge(from, to string) error {
	return g.addEdge(from, to, true)
}

func (g *Graph) addEdge(from, to string, serialize bool) error {
	fromName, err := g.resolveNodeRef(from)
	if err != nil {
		return err
	}
	toName, err := g.resolveEdgeTarget(to)
	if err != nil {
		return err
	}
	if _, exists := g.edges[fromName]; exists {
		return fmt.Errorf("node %q already has a default edge", fromName)
	}
	g.edges[fromName] = toName
	if serialize {
		g.edgeSpecs = append(g.edgeSpecs, GraphEdgeSpec{
			From: g.nodeSpecs[fromName].ID,
			To:   g.serializeNodeRef(toName),
		})
	}
	return nil
}

func (g *Graph) AddConditionalEdge(from, to string, condition EdgeCondition) error {
	return g.addConditionalEdgeWithSpec(from, to, nil, condition, true)
}

func (g *Graph) AddConditionalEdgeWithSpec(from, to string, conditionSpec GraphConditionSpec, condition EdgeCondition) error {
	return g.addConditionalEdgeWithSpec(from, to, &conditionSpec, condition, true)
}

func (g *Graph) addConditionalEdgeWithSpec(from, to string, conditionSpec *GraphConditionSpec, condition EdgeCondition, serialize bool) error {
	if condition == nil {
		return fmt.Errorf("condition is nil")
	}

	fromName, err := g.resolveNodeRef(from)
	if err != nil {
		return err
	}
	toName, err := g.resolveEdgeTarget(to)
	if err != nil {
		return err
	}

	g.conditionalEdges[fromName] = append(g.conditionalEdges[fromName], conditionalEdge{
		to:        toName,
		when:      condition,
		condition: conditionSpec,
	})
	if serialize {
		edgeSpec := GraphEdgeSpec{
			From: g.nodeSpecs[fromName].ID,
			To:   g.serializeNodeRef(toName),
		}
		if conditionSpec != nil {
			copySpec := *conditionSpec
			if len(copySpec.Config) > 0 {
				copySpec.Config = cloneMap(copySpec.Config)
			}
			edgeSpec.Condition = &copySpec
		}
		g.edgeSpecs = append(g.edgeSpecs, edgeSpec)
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
	name, err := g.resolveNodeRef(nodeRef)
	if err != nil {
		return err
	}
	g.nodeListeners[name] = append(g.nodeListeners[name], listener)
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

	for name, node := range g.nodes {
		nodeDef := node
		listenableNode := compiled.AddNode(name, node.Description(), func(ctx context.Context, state State) (State, error) {
			return nodeDef.Invoke(ctx, state.CloneState())
		})
		for _, listener := range g.nodeListeners[name] {
			listenableNode.AddListener(listener)
		}
	}

	for _, listener := range g.globalListeners {
		compiled.AddGlobalListener(listener)
	}

	for from, conditional := range g.conditionalEdges {
		edges := append([]conditionalEdge(nil), conditional...)
		defaultTarget, hasDefaultTarget := g.edges[from]
		isFinishPoint := from == g.finishPoint

		compiled.AddConditionalEdge(from, func(ctx context.Context, state State) string {
			for _, edge := range edges {
				if edge.when(ctx, state) {
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

func (g *Graph) Run(ctx context.Context, initialState State) (State, error) {
	runnable, err := g.Compile()
	if err != nil {
		return initialState, err
	}
	return runnable.Invoke(ctx, initialState)
}

func (g *Graph) resolveNodeRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("node reference is empty")
	}
	if _, ok := g.nodes[ref]; ok {
		return ref, nil
	}
	if name, ok := g.nodeIDs[ref]; ok {
		return name, nil
	}
	return "", fmt.Errorf("node %q not found", ref)
}

func (g *Graph) resolveEdgeTarget(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("edge target is empty")
	}
	if ref == langgraph.END || ref == EndNodeRef {
		return langgraph.END, nil
	}
	return g.resolveNodeRef(ref)
}

func (g *Graph) Definition() (GraphDefinition, error) {
	if err := g.Validate(); err != nil {
		return GraphDefinition{}, err
	}

	nodeNames := make([]string, 0, len(g.nodes))
	for name := range g.nodes {
		nodeNames = append(nodeNames, name)
	}
	sort.Slice(nodeNames, func(i, j int) bool {
		left := g.nodeSpecs[nodeNames[i]]
		right := g.nodeSpecs[nodeNames[j]]
		if left.ID == right.ID {
			return left.Name < right.Name
		}
		return left.ID < right.ID
	})

	nodes := make([]GraphNodeSpec, 0, len(nodeNames))
	for _, name := range nodeNames {
		spec := g.nodeSpecs[name]
		if spec.Type == "" {
			return GraphDefinition{}, fmt.Errorf("node %q is not serializable: missing registered type", name)
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
		StateSchema: CommonStateSchemaID,
		EntryPoint:  g.serializeNodeRef(g.entryPoint),
		FinishPoint: g.serializeNodeRef(g.finishPoint),
		Nodes:       nodes,
		Edges:       edges,
	}, nil
}

func (g *Graph) serializeNodeRef(name string) string {
	if name == "" {
		return ""
	}
	if name == langgraph.END {
		return EndNodeRef
	}
	if spec, ok := g.nodeSpecs[name]; ok && spec.ID != "" {
		return spec.ID
	}
	return name
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
