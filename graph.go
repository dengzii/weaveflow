package falcon

import (
	"context"
	"fmt"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

type EdgeCondition[S BaseState] func(ctx context.Context, state S) (bool, error)

type conditionalEdge[S BaseState] struct {
	to   string
	when EdgeCondition[S]
}

type Graph[S BaseState] struct {
	nodes            map[string]NodeDefinition[S]
	nodeNames        map[string][]string
	edges            map[string]string
	conditionalEdges map[string][]conditionalEdge[S]
	entryPoint       string
	finishPoint      string
	stepLimit        int
}

func NewGraph[S BaseState]() *Graph[S] {
	g := &Graph[S]{
		nodes:            map[string]NodeDefinition[S]{},
		nodeNames:        map[string][]string{},
		edges:            map[string]string{},
		conditionalEdges: map[string][]conditionalEdge[S]{},
	}

	g.entryPoint = IdStartNode
	g.finishPoint = IdEndNode

	return g
}

func (g *Graph[S]) AddNode(node NodeDefinition[S]) error {
	if node == nil {
		return fmt.Errorf("node is nil")
	}

	id := strings.TrimSpace(resolveOrCreateNodeID(node))
	if id == "" {
		return fmt.Errorf("node id is empty")
	}
	if _, exists := g.nodes[id]; exists {
		return fmt.Errorf("node %q already exists", id)
	}

	name := strings.TrimSpace(node.Name())
	if name == "" {
		return fmt.Errorf("node name is empty")
	}

	g.nodes[id] = node
	g.nodeNames[name] = append(g.nodeNames[name], id)
	return nil
}

func (g *Graph[S]) SetEntryPoint(name string) error {
	return g.setSpecialPoint(name, true)
}

func (g *Graph[S]) SetFinishPoint(name string) error {
	return g.setSpecialPoint(name, false)
}

func (g *Graph[S]) SetStepLimit(limit int) {
	g.stepLimit = limit
}

func (g *Graph[S]) AddEdge(from, to string) error {
	fromID, err := g.resolveNodeRef(from)
	if err != nil {
		return err
	}
	toID, err := g.resolveNodeRef(to)
	if err != nil {
		return err
	}
	if _, exists := g.edges[fromID]; exists {
		return fmt.Errorf("node %q already has a default edge", g.nodeLabel(fromID))
	}

	g.edges[fromID] = toID
	return nil
}

func (g *Graph[S]) AddConditionalEdge(from, to string, condition EdgeCondition[S]) error {
	fromID, err := g.resolveNodeRef(from)
	if err != nil {
		return err
	}
	toID, err := g.resolveNodeRef(to)
	if err != nil {
		return err
	}
	if condition == nil {
		return fmt.Errorf("condition is nil")
	}

	g.conditionalEdges[fromID] = append(g.conditionalEdges[fromID], conditionalEdge[S]{
		to:   toID,
		when: condition,
	})
	return nil
}

func (g *Graph[S]) Validate() error {
	if _, err := g.resolveNodeRef(g.entryPoint); err != nil {
		return fmt.Errorf("invalid entry point: %w", err)
	}
	if _, err := g.resolveNodeRef(g.finishPoint); err != nil {
		return fmt.Errorf("invalid finish point: %w", err)
	}

	reachable := map[string]bool{}
	queue := []string{g.entryPoint}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if reachable[current] {
			continue
		}
		reachable[current] = true

		for _, next := range g.outgoing(current) {
			if !reachable[next] {
				queue = append(queue, next)
			}
		}
	}

	if !reachable[g.finishPoint] {
		return fmt.Errorf("finish point %q is unreachable from %q", g.nodeLabel(g.finishPoint), g.nodeLabel(g.entryPoint))
	}

	for nodeID := range reachable {
		if nodeID == g.finishPoint {
			continue
		}
		if len(g.outgoing(nodeID)) == 0 {
			return fmt.Errorf("node %q has no outgoing edge", g.nodeLabel(nodeID))
		}
	}

	return nil
}

func (g *Graph[S]) Run(ctx context.Context, state S) (S, error) {
	if err := g.Validate(); err != nil {
		return state, err
	}

	current := g.entryPoint
	steps := 0
	limit := g.resolveStepLimit(state)

	for {
		if err := ctx.Err(); err != nil {
			return state, err
		}

		node, ok := g.nodes[current]
		if !ok {
			return state, fmt.Errorf("node %q not found", g.nodeLabel(current))
		}

		nextState, err := node.Invoke(ctx, state)
		if err != nil {
			return state, fmt.Errorf("invoke node %q: %w", g.nodeLabel(current), err)
		}
		state = nextState

		if current == g.finishPoint {
			return state, nil
		}

		next, err := g.nextNode(ctx, current, state)
		if err != nil {
			return state, err
		}

		steps++
		if limit > 0 && steps > limit {
			return state, fmt.Errorf("graph exceeded step limit %d, last node %q", limit, g.nodeLabel(current))
		}
		current = next
	}
}

func LastMessageHasToolCalls[S BaseState](ctx context.Context, state S) (bool, error) {
	_ = ctx

	messages := state.GetMessages()
	if len(messages) == 0 {
		return false, nil
	}

	lastMessage := messages[len(messages)-1]
	if lastMessage.Role != llms.ChatMessageTypeAI {
		return false, nil
	}

	for _, part := range lastMessage.Parts {
		if _, ok := part.(llms.ToolCall); ok {
			return true, nil
		}
	}

	return false, nil
}

func HasFinalAnswer[S BaseState](ctx context.Context, state S) (bool, error) {
	_ = ctx
	return strings.TrimSpace(state.FinalAnswer()) != "", nil
}

func Always[S BaseState](ctx context.Context, state S) (bool, error) {
	_ = ctx
	_ = state
	return true, nil
}

func (g *Graph[S]) nextNode(ctx context.Context, current string, state S) (string, error) {
	for _, edge := range g.conditionalEdges[current] {
		matched, err := edge.when(ctx, state)
		if err != nil {
			return "", fmt.Errorf("evaluate conditional edge from %q to %q: %w", g.nodeLabel(current), g.nodeLabel(edge.to), err)
		}
		if matched {
			return edge.to, nil
		}
	}

	if next, ok := g.edges[current]; ok {
		return next, nil
	}

	return "", fmt.Errorf("node %q has no matching outgoing edge", g.nodeLabel(current))
}

func (g *Graph[S]) outgoing(nodeID string) []string {
	next := make([]string, 0, len(g.conditionalEdges[nodeID])+1)
	if to, ok := g.edges[nodeID]; ok {
		next = append(next, to)
	}
	for _, edge := range g.conditionalEdges[nodeID] {
		next = append(next, edge.to)
	}
	return next
}

func (g *Graph[S]) resolveNodeRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("node reference is empty")
	}
	if _, ok := g.nodes[ref]; ok {
		return ref, nil
	}

	ids := g.nodeNames[ref]
	switch len(ids) {
	case 0:
		return "", fmt.Errorf("node %q not found", ref)
	case 1:
		return ids[0], nil
	default:
		return "", fmt.Errorf("node name %q is ambiguous; use node ID instead: %s", ref, strings.Join(ids, ", "))
	}
}

func (g *Graph[S]) setSpecialPoint(name string, entry bool) error {
	nodeID, err := g.resolveNodeRef(name)
	if err != nil {
		return err
	}

	if entry {
		g.entryPoint = nodeID
		return nil
	}

	g.finishPoint = nodeID
	return nil
}

func (g *Graph[S]) nodeLabel(nodeID string) string {
	node, ok := g.nodes[nodeID]
	if !ok {
		return nodeID
	}
	return fmt.Sprintf("%s (%s)", node.Name(), nodeID)
}

func resolveOrCreateNodeID[S BaseState](node NodeDefinition[S]) string {
	if ensured, ok := any(node).(interface{ EnsureID() string }); ok {
		return ensured.EnsureID()
	}
	return node.ID()
}

func (g *Graph[S]) resolveStepLimit(state S) int {
	if g.stepLimit > 0 {
		return g.stepLimit
	}

	nodeCount := len(g.nodes)
	if nodeCount == 0 {
		return 0
	}

	maxIterations := state.MaxIterations()
	if maxIterations <= 0 {
		return nodeCount*8 + 8
	}

	return maxIterations*maxInt(nodeCount, 2) + nodeCount*2 + 2
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
