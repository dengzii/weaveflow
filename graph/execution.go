package graph

import (
	"context"
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/state"
)

func (g *Graph) resolveNextNodes(ctx context.Context, currentNodeID string, currentState *state.State) ([]string, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	currentNodeID = strings.TrimSpace(currentNodeID)
	if currentNodeID == "" {
		return nil, fmt.Errorf("node id is empty")
	}
	if currentNodeID != endNodeID {
		if _, ok := g.nodes[currentNodeID]; !ok {
			return nil, fmt.Errorf("node id %q not found", currentNodeID)
		}
	}
	if conditional := g.conditionalEdges[currentNodeID]; len(conditional) > 0 {
		for _, edge := range conditional {
			conditionState := currentState
			if edge.resolved {
				if issues := state.ValidateRequiredReads(currentState, edge.contract); len(issues) > 0 {
					return nil, fmt.Errorf("condition %q on edge %q -> %q state contract violation: %s", edge.condition.Spec.Type, currentNodeID, g.serializeNodeRef(edge.to), issues[0].Message)
				}
				conditionState = state.ProjectStateByContract(currentState, edge.contract)
			}
			if edge.condition.Match(ctx, conditionState) {
				return []string{edge.to}, nil
			}
		}
		if targets := g.defaultEdges[currentNodeID]; len(targets) > 0 {
			return []string{targets[0]}, nil
		}
		if currentNodeID == g.finishPoint {
			return []string{endNodeID}, nil
		}
		return nil, fmt.Errorf("node %q produced no matching conditional edge", currentNodeID)
	}
	if targets := g.defaultEdges[currentNodeID]; len(targets) > 0 {
		return append([]string(nil), targets...), nil
	}
	if currentNodeID == g.finishPoint {
		return []string{endNodeID}, nil
	}
	return nil, fmt.Errorf("node %q has no outgoing edge", currentNodeID)
}

func (g *Graph) Run(ctx context.Context, initialState *state.State) (*state.State, error) {
	if g == nil {
		return initialState, fmt.Errorf("graph is nil")
	}
	runnable, err := g.Compile()
	if err != nil {
		return initialState, err
	}
	return runnable.Invoke(ctx, initialState)
}
