package graph

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dengzii/weaveflow/core"
	fruntime "github.com/dengzii/weaveflow/runtime"
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
					return nil, g.conditionError(currentNodeID, edge, fmt.Errorf("state contract violation: %s", issues[0].Message))
				}
				conditionState = state.ProjectStateByContract(currentState, edge.contract)
			}
			matched, err := edge.condition.Match(ctx, conditionState)
			if err != nil {
				return nil, g.conditionError(currentNodeID, edge, err)
			}
			if matched {
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

type ConditionError struct {
	ConditionID   string
	ConditionType string
	SourceNodeID  string
	TargetNodeID  string
	StatePaths    []string
	Err           error
}

func (conditionErr *ConditionError) Error() string {
	if conditionErr == nil {
		return "condition evaluation failed"
	}
	return fmt.Sprintf("condition %q (%s) on edge %q -> %q failed for state paths %v: %v", conditionErr.ConditionID, conditionErr.ConditionType, conditionErr.SourceNodeID, conditionErr.TargetNodeID, conditionErr.StatePaths, conditionErr.Err)
}

func (conditionErr *ConditionError) Unwrap() error {
	if conditionErr == nil {
		return nil
	}
	return conditionErr.Err
}

func (conditionErr *ConditionError) Class() core.ErrorClass {
	return core.ErrorNonRetryable
}

func (conditionErr *ConditionError) RetryAfter() time.Duration { return 0 }

func (conditionErr *ConditionError) Details() map[string]any {
	if conditionErr == nil {
		return nil
	}
	return map[string]any{
		"condition_id":   conditionErr.ConditionID,
		"condition_type": conditionErr.ConditionType,
		"source_node_id": conditionErr.SourceNodeID,
		"target_node_id": conditionErr.TargetNodeID,
		"state_paths":    append([]string(nil), conditionErr.StatePaths...),
	}
}

func (g *Graph) conditionError(sourceNodeID string, edge conditionalEdge, cause error) *ConditionError {
	spec := edge.condition.CloneSpec()
	targetNodeID := g.serializeNodeRef(edge.to)
	conditionID := strings.TrimSpace(spec.ID)
	if conditionID == "" {
		conditionID = fmt.Sprintf("%s->%s:%s", sourceNodeID, targetNodeID, spec.Type)
	}
	paths := make([]string, 0, len(spec.State))
	for _, binding := range spec.State {
		if path := strings.TrimSpace(binding.Path); path != "" {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return &ConditionError{
		ConditionID:   conditionID,
		ConditionType: spec.Type,
		SourceNodeID:  sourceNodeID,
		TargetNodeID:  targetNodeID,
		StatePaths:    paths,
		Err:           cause,
	}
}

func conditionSchedulerEvent(err error) *fruntime.SchedulerEvent {
	var conditionErr *ConditionError
	if !errors.As(err, &conditionErr) {
		return nil
	}
	payload := conditionErr.Details()
	payload["error"] = conditionErr.Error()
	payload["error_class"] = conditionErr.Class()
	return &fruntime.SchedulerEvent{Type: fruntime.SchedulerEventConditionFailed, NodeID: conditionErr.SourceNodeID, Payload: payload}
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
