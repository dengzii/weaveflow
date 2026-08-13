package graph

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/registry"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

func (g *Graph) resolveNextNodes(ctx context.Context, currentNodeID string, currentState *state.State) ([]string, error) {
	tasks, err := g.resolveNextTasksObserved(ctx, fruntime.NewStaticGraphTask(currentNodeID, 0), currentState, nil)
	if err != nil {
		return nil, err
	}
	return fruntime.GraphTaskNodeIDs(tasks), nil
}

func (g *Graph) resolveNextTasksObserved(ctx context.Context, parent fruntime.GraphTask, currentState *state.State, observe func(registry.RouteDecision) error) ([]fruntime.GraphTask, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	currentNodeID := strings.TrimSpace(parent.NodeID)
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
			decision, err := edge.condition.EvaluateRoute(ctx, conditionState)
			if err != nil {
				return nil, g.conditionError(currentNodeID, edge, err)
			}
			if observe != nil {
				if err := observe(decision); err != nil {
					return nil, err
				}
			}
			if decision.Matched {
				return g.tasksFromRouteDecision(parent, edge.to, decision)
			}
		}
		if targets := g.defaultEdges[currentNodeID]; len(targets) > 0 {
			return staticRouteTasks(targets[:1]), nil
		}
		if currentNodeID == g.finishPoint {
			return staticRouteTasks([]string{endNodeID}), nil
		}
		return nil, fmt.Errorf("node %q produced no matching conditional edge", currentNodeID)
	}
	if targets := g.defaultEdges[currentNodeID]; len(targets) > 0 {
		return staticRouteTasks(targets), nil
	}
	if currentNodeID == g.finishPoint {
		return staticRouteTasks([]string{endNodeID}), nil
	}
	return nil, fmt.Errorf("node %q has no outgoing edge", currentNodeID)
}

func (g *Graph) tasksFromRouteDecision(parent fruntime.GraphTask, edgeTarget string, decision registry.RouteDecision) ([]fruntime.GraphTask, error) {
	if len(decision.Send) > 0 {
		tasks := make([]fruntime.GraphTask, 0, len(decision.Send))
		for index, send := range decision.Send {
			nodeID, err := g.resolveNodeID(string(send.Target))
			if err != nil {
				return nil, fmt.Errorf("route decision send target %q: %w", send.Target, err)
			}
			tasks = append(tasks, fruntime.GraphTask{
				TaskID:         dynamicTaskID(parent, send, index),
				NodeID:         nodeID,
				Input:          send.Input,
				CorrelationKey: strings.TrimSpace(send.CorrelationKey),
				OrderKey:       strings.TrimSpace(send.OrderKey),
				Order:          index,
				Dynamic:        true,
			})
		}
		sort.SliceStable(tasks, func(leftIndex, rightIndex int) bool {
			left, right := tasks[leftIndex], tasks[rightIndex]
			if left.OrderKey != right.OrderKey {
				return left.OrderKey < right.OrderKey
			}
			if left.CorrelationKey != right.CorrelationKey {
				return left.CorrelationKey < right.CorrelationKey
			}
			return left.TaskID < right.TaskID
		})
		for index := range tasks {
			tasks[index].Order = index
		}
		return tasks, nil
	}
	if len(decision.Targets) == 0 {
		return staticRouteTasks([]string{edgeTarget}), nil
	}
	targets := make([]string, 0, len(decision.Targets))
	for _, target := range decision.Targets {
		nodeID, err := g.resolveEdgeTarget(string(target))
		if err != nil {
			return nil, fmt.Errorf("route decision target %q: %w", target, err)
		}
		targets = append(targets, nodeID)
	}
	return staticRouteTasks(targets), nil
}

func staticRouteTasks(targets []string) []fruntime.GraphTask {
	tasks := make([]fruntime.GraphTask, 0, len(targets))
	for index, nodeID := range targets {
		tasks = append(tasks, fruntime.NewStaticGraphTask(nodeID, index))
	}
	return tasks
}

func routeDecisionSchedulerEvent(sourceNodeID string, decision registry.RouteDecision) fruntime.SchedulerEvent {
	payload := map[string]any{"matched": decision.Matched}
	if len(decision.Targets) > 0 {
		targets := make([]string, len(decision.Targets))
		for index, target := range decision.Targets {
			targets[index] = string(target)
		}
		payload["targets"] = targets
	}
	if len(decision.Send) > 0 {
		sends := make([]map[string]any, len(decision.Send))
		for index, send := range decision.Send {
			sends[index] = map[string]any{
				"target":          string(send.Target),
				"correlation_key": strings.TrimSpace(send.CorrelationKey),
				"order_key":       strings.TrimSpace(send.OrderKey),
			}
		}
		payload["sends"] = sends
	}
	if decision.Reason != "" {
		payload["reason"] = decision.Reason
	}
	if len(decision.Details) > 0 {
		payload["details"] = decision.Details
	}
	return fruntime.SchedulerEvent{Type: fruntime.SchedulerEventRouteDecision, NodeID: sourceNodeID, Payload: payload}
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

func failureRoutedSchedulerEvent(source fruntime.GraphTask, cause error, next []fruntime.GraphTask) fruntime.SchedulerEvent {
	payload := map[string]any{
		"source_task_id": source.TaskID,
		"source_node_id": source.NodeID,
		"next_node_ids":  fruntime.GraphTaskNodeIDs(next),
	}
	if cause != nil {
		payload["error"] = cause.Error()
		payload["error_class"] = core.ClassifyError(cause)
	}
	if len(next) > 0 && next[0].Failure != nil {
		failure := next[0].Failure
		payload["stage"] = failure.Stage
		payload["details"] = failure.Details
	}
	return fruntime.SchedulerEvent{Type: fruntime.SchedulerEventFailureRouted, NodeID: source.NodeID, Payload: payload}
}

func (g *Graph) resolveFailure(_ context.Context, task fruntime.GraphTask, stage string, err error) ([]fruntime.GraphTask, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}
	if err == nil {
		return nil, nil
	}
	if !failureRoutable(stage, err) {
		return nil, nil
	}
	sourceNodeID := task.NodeID
	if stage == string(dsl.FailureStageCondition) {
		var conditionErr *ConditionError
		if errors.As(err, &conditionErr) {
			sourceNodeID = conditionErr.SourceNodeID
		}
	}
	errorClass := core.ClassifyError(err)
	for _, route := range g.failureRoutes[sourceNodeID] {
		if !failureRouteMatches(route.route, stage, errorClass) {
			continue
		}
		failure := &fruntime.FailureContext{Stage: stage, ErrorClass: errorClass, Error: err.Error(), SourceNodeID: sourceNodeID}
		var classified core.ExecutionError
		if errors.As(err, &classified) {
			failure.Details = classified.Details()
		}
		return []fruntime.GraphTask{{TaskID: fmt.Sprintf("failure-%s", task.TaskID), NodeID: route.to, Order: task.Order, Failure: failure}}, nil
	}
	return nil, nil
}

func failureRoutable(stage string, err error) bool {
	if stage != string(dsl.FailureStageNode) && stage != string(dsl.FailureStageCondition) {
		return false
	}
	if stage == string(dsl.FailureStageCondition) {
		var conditionErr *ConditionError
		if !errors.As(err, &conditionErr) {
			return false
		}
		if errors.Is(conditionErr.Err, context.Canceled) {
			return false
		}
		var classified core.ExecutionError
		if errors.As(conditionErr.Err, &classified) && core.ClassifyError(conditionErr.Err) == core.ErrorResourceExhausted {
			return false
		}
		return true
	}
	var classified core.ExecutionError
	if !errors.As(err, &classified) {
		return false
	}
	switch core.ClassifyError(err) {
	case core.ErrorCanceled, core.ErrorResourceExhausted:
		return false
	default:
		return true
	}
}

func failureRouteMatches(route dsl.FailureRouteSpec, stage string, class core.ErrorClass) bool {
	if !route.CatchAll && len(route.Stages) == 0 && len(route.ErrorClasses) == 0 {
		return false
	}
	if len(route.Stages) > 0 {
		matched := false
		for _, candidate := range route.Stages {
			if string(candidate) == stage {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if len(route.ErrorClasses) > 0 {
		matched := false
		for _, candidate := range route.ErrorClasses {
			if strings.TrimSpace(candidate) == string(class) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
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
