package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dengzii/weaveflow/core"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

type scheduledNodeExecutor func(context.Context, string, *state.State) (*state.State, error)
type scheduledNodePreparer func(context.Context, string, *state.State) (context.Context, error)

type scheduledRunnable struct {
	graph        *Graph
	patches      *compilePatchCollector
	prepareNode  scheduledNodePreparer
	executeNode  scheduledNodeExecutor
	observeNode  func(context.Context, NodeEvent, string, *state.State, error, time.Duration)
	joinNodes    map[string]struct{}
	reachability map[string]map[string]bool
}

type executionBudget struct {
	maxNodeExecutions int64
	nodeExecutions    atomic.Int64
	superSteps        atomic.Int64
	parentContext     context.Context
	maxWallTime       time.Duration
	elapsedWallTime   time.Duration
	startedAt         time.Time
}

func newScheduledRunnable(targetGraph *Graph, patches *compilePatchCollector, executeNode scheduledNodeExecutor) *scheduledRunnable {
	return &scheduledRunnable{
		graph:        targetGraph,
		patches:      patches,
		executeNode:  executeNode,
		joinNodes:    targetGraph.compileJoinNodes(),
		reachability: targetGraph.compileReachability(),
	}
}

func (runnable *scheduledRunnable) Invoke(ctx context.Context, initialState *state.State) (*state.State, error) {
	return runnable.InvokeWithConfig(ctx, initialState, fruntime.SchedulerConfig{})
}

func (runnable *scheduledRunnable) InvokeWithConfig(ctx context.Context, initialState *state.State, config fruntime.SchedulerConfig) (*state.State, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return runnable.invokeWithConfig(ctx, initialState, config)
}

func (runnable *scheduledRunnable) invokeWithConfig(ctx context.Context, initialState *state.State, config fruntime.SchedulerConfig) (*state.State, error) {
	if runnable == nil || runnable.graph == nil {
		return nil, fmt.Errorf("scheduled graph is nil")
	}
	if runnable.executeNode == nil {
		return nil, fmt.Errorf("scheduled graph node executor is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	policy := runnable.graph.executionPolicy
	releaseRun, err := runnable.acquireConcurrency(ctx, config, runnable.graph.runLimiter, "run", "")
	if err != nil {
		return initialState, err
	}
	defer releaseRun()
	currentState := state.NewState()
	if initialState != nil {
		currentState = initialState.Clone()
	}
	restoredBudget, _ := fruntime.LoadGraphExecutionBudget(currentState)
	remainingWallTime := policy.Limits.MaxWallTime - restoredBudget.ElapsedWallTime
	if remainingWallTime <= 0 {
		limitErr := core.NewExecutionError(core.ErrorTimeout, "graph wall-time limit exceeded", nil, map[string]any{
			"limit":  policy.Limits.MaxWallTime.String(),
			"actual": restoredBudget.ElapsedWallTime.String(),
			"kind":   "wall_time",
		})
		return currentState, runnable.notifyLimitExceeded(ctx, config, "", limitErr)
	}
	parentCtx := ctx
	ctx, cancel := context.WithTimeout(parentCtx, remainingWallTime)
	defer cancel()
	ctx = core.WithToolConcurrencyLimiter(ctx, runnable.graph.toolLimiter, func(limit int) {
		_ = runnable.notifyBackpressure(context.WithoutCancel(ctx), config, "tool", "", limit)
	})
	currentNodes := []string{runnable.graph.entryPoint}
	if len(config.StartNodeIDs) > 0 {
		currentNodes = append([]string(nil), config.StartNodeIDs...)
	}
	_, restoredPending, _ := fruntime.LoadGraphSchedule(currentState)
	pendingFanIn := nodeIDSet(restoredPending)
	currentNodes, pendingFanIn = runnable.resumeSchedule(currentNodes, pendingFanIn)
	budget := &executionBudget{
		maxNodeExecutions: int64(policy.Limits.MaxNodeExecutions),
		parentContext:     parentCtx,
		maxWallTime:       policy.Limits.MaxWallTime,
		elapsedWallTime:   restoredBudget.ElapsedWallTime,
		startedAt:         time.Now(),
	}
	budget.nodeExecutions.Store(restoredBudget.NodeExecutions)
	budget.superSteps.Store(restoredBudget.SuperSteps)
	if err := runnable.storeExecutionBudget(currentState, budget); err != nil {
		return currentState, err
	}
	if err := runnable.validateStateSize(ctx, config, currentState); err != nil {
		return currentState, err
	}

	for {
		if err := ctx.Err(); err != nil {
			if parentCtx.Err() != nil {
				return currentState, parentCtx.Err()
			}
			limitErr := core.NewExecutionError(core.ErrorTimeout, "graph wall-time limit exceeded", err, map[string]any{
				"limit": policy.Limits.MaxWallTime.String(),
				"kind":  "wall_time",
			})
			return currentState, runnable.notifyLimitExceeded(ctx, config, "", limitErr)
		}
		currentNodes = activeNodeIDs(currentNodes)
		if len(currentNodes) == 0 {
			break
		}
		superSteps := budget.superSteps.Add(1)
		if superSteps > int64(policy.Limits.MaxSuperSteps) {
			limitErr := core.NewExecutionError(core.ErrorResourceExhausted, "graph super-step limit exceeded", nil, map[string]any{
				"limit":  policy.Limits.MaxSuperSteps,
				"actual": superSteps,
				"kind":   "super_steps",
			})
			return currentState, runnable.notifyLimitExceeded(ctx, config, "", limitErr)
		}
		if len(currentNodes) > policy.Limits.MaxFanOut {
			limitErr := core.NewExecutionError(core.ErrorResourceExhausted, "graph fan-out limit exceeded", nil, map[string]any{
				"limit":  policy.Limits.MaxFanOut,
				"actual": len(currentNodes),
				"kind":   "fan_out",
			})
			return currentState, runnable.notifyLimitExceeded(ctx, config, "", limitErr)
		}
		if err := fruntime.StoreGraphSchedule(currentState, nil, sortedNodeIDSet(pendingFanIn)); err != nil {
			return currentState, err
		}
		if err := runnable.storeExecutionBudget(currentState, budget); err != nil {
			return currentState, err
		}

		results, nodeErrors := runnable.executeNodes(ctx, config, budget, currentNodes, currentState)
		mergedState, err := runnable.graph.mergeCompiledStates(ctx, currentState, results, runnable.patches)
		if err != nil {
			mergeErr := fmt.Errorf("state merge failed: %w", err)
			return currentState, mergeErr
		}
		currentState = mergedState
		if err := runnable.storeExecutionBudget(currentState, budget); err != nil {
			return currentState, err
		}
		if err := runnable.validateStateSize(ctx, config, currentState); err != nil {
			return currentState, err
		}

		if interrupt := firstNodeInterrupt(currentNodes, nodeErrors); interrupt != nil {
			if err := runnable.notifyGraphStep(ctx, config, currentNodes, currentState); err != nil {
				return currentState, err
			}
			return currentState, &fruntime.GraphInterrupt{
				NodeID:      interrupt.NodeID,
				State:       currentState,
				Value:       interrupt.Value,
				NextNodeIDs: []string{interrupt.NodeID},
			}
		}
		if nodeErr := firstError(nodeErrors); nodeErr != nil {
			return currentState, nodeErr
		}

		nodesRan := append([]string(nil), currentNodes...)
		nextNodes, err := runnable.resolveNextNodes(ctx, nodesRan, currentState)
		if err != nil {
			if event := conditionSchedulerEvent(err); event != nil {
				if notifyErr := runnable.notifySchedulerEvent(context.WithoutCancel(ctx), config, *event); notifyErr != nil {
					return currentState, errors.Join(err, notifyErr)
				}
			}
			return currentState, err
		}
		currentNodes, pendingFanIn = runnable.scheduleNext(nextNodes, pendingFanIn)
		if err := fruntime.StoreGraphSchedule(currentState, currentNodes, sortedNodeIDSet(pendingFanIn)); err != nil {
			return currentState, err
		}
		if err := runnable.notifyGraphStep(ctx, config, nodesRan, currentState); err != nil {
			return currentState, err
		}

		if interruptedNode := configuredInterruptNode(nodesRan, config, false); interruptedNode != "" {
			return currentState, &fruntime.GraphInterrupt{
				NodeID:      interruptedNode,
				State:       currentState,
				NextNodeIDs: append([]string(nil), currentNodes...),
			}
		}
	}

	if err := fruntime.ClearGraphSchedule(currentState); err != nil {
		return currentState, err
	}
	return currentState, nil
}

func (runnable *scheduledRunnable) executeNodes(ctx context.Context, config fruntime.SchedulerConfig, budget *executionBudget, nodeIDs []string, currentState *state.State) ([]*state.State, []error) {
	results := make([]*state.State, len(nodeIDs))
	nodeErrors := make([]error, len(nodeIDs))
	type nodeTask struct {
		index  int
		nodeID string
	}
	tasks := make(chan nodeTask, len(nodeIDs))
	var waitGroup sync.WaitGroup
	workerCount := min(len(nodeIDs), runnable.graph.executionPolicy.Limits.MaxConcurrentNodes)
	waitGroup.Add(workerCount)
	for range workerCount {
		go func() {
			defer waitGroup.Done()
			for task := range tasks {
				func() {
					startedAt := time.Now()
					runnable.notifyNode(ctx, EventNodeStart, task.nodeID, currentState, nil, 0)
					defer func() {
						if recovered := recover(); recovered != nil {
							results[task.index] = currentState
							nodeErrors[task.index] = fmt.Errorf("panic in node %s: %v", task.nodeID, recovered)
							recordFailedBranchPatch(runnable.patches, currentState, task.nodeID, nodeErrors[task.index])
							runnable.notifyNode(ctx, EventNodeError, task.nodeID, currentState, nodeErrors[task.index], time.Since(startedAt))
						}
					}()
					result, err := runnable.executeNodeWithRetry(ctx, config, budget, task.nodeID, currentState)
					if result == nil {
						result = currentState
					}
					results[task.index] = result
					if err != nil {
						nodeErrors[task.index] = fmt.Errorf("error in node %s: %w", task.nodeID, err)
						recordFailedBranchPatch(runnable.patches, currentState, task.nodeID, nodeErrors[task.index])
						runnable.notifyNode(ctx, EventNodeError, task.nodeID, result, nodeErrors[task.index], time.Since(startedAt))
						return
					}
					runnable.notifyNode(ctx, EventNodeComplete, task.nodeID, result, nil, time.Since(startedAt))
				}()
			}
		}()
	}
	for index, nodeID := range nodeIDs {
		tasks <- nodeTask{index: index, nodeID: nodeID}
	}
	close(tasks)
	waitGroup.Wait()
	return results, nodeErrors
}

func (runnable *scheduledRunnable) notifyNode(ctx context.Context, event NodeEvent, nodeID string, currentState *state.State, err error, duration time.Duration) {
	if runnable.observeNode != nil {
		runnable.observeNode(ctx, event, nodeID, currentState, err, duration)
	}
}

func (runnable *scheduledRunnable) executeNodeWithRetry(ctx context.Context, config fruntime.SchedulerConfig, budget *executionBudget, nodeID string, currentState *state.State) (*state.State, error) {
	policy := runnable.graph.nodeExecutionPolicy(nodeID)
	var lastResult *state.State
	var lastErr error
	var timedOutAttempt <-chan attemptResult
	for attempt := 1; attempt <= policy.Retry.MaxAttempts; attempt++ {
		if timedOutAttempt != nil {
			select {
			case <-timedOutAttempt:
				timedOutAttempt = nil
			case <-ctx.Done():
				if budget.parentContext != nil && budget.parentContext.Err() != nil {
					completed := <-timedOutAttempt
					if completed.state != nil {
						lastResult = completed.state
					}
				}
				return lastResult, runnable.executionContextError(ctx, config, budget, nodeID)
			}
		}
		lastErr = nil
		executionCtx := fruntime.WithGraphExecutionBudgetProvider(ctx, budget.snapshot)
		if runnable.prepareNode != nil {
			var err error
			executionCtx, err = runnable.prepareNode(executionCtx, nodeID, currentState)
			if err != nil {
				lastErr = err
			}
		}
		if lastErr == nil {
			if err := budget.claimNodeExecution(); err != nil {
				return lastResult, runnable.notifyLimitExceeded(ctx, config, nodeID, err)
			}
			releaseGraphNode, err := runnable.acquireConcurrency(executionCtx, config, runnable.graph.nodeLimiter, "graph_node", nodeID)
			if err != nil {
				return lastResult, err
			}
			releaseNode, err := runnable.acquireConcurrency(executionCtx, config, runnable.graph.nodeLimiters[nodeID], "node", nodeID)
			if err != nil {
				releaseGraphNode()
				return lastResult, err
			}
			attemptCtx, cancel := context.WithTimeout(executionCtx, policy.Timeout)
			result := make(chan attemptResult, 1)
			go func() {
				completed := attemptResult{state: currentState}
				defer func() {
					if recovered := recover(); recovered != nil {
						completed.err = fmt.Errorf("panic in node %s: %v", nodeID, recovered)
					}
					releaseNode()
					releaseGraphNode()
					result <- completed
				}()
				completed.state, completed.err = runnable.executeNode(attemptCtx, nodeID, currentState)
			}()
			select {
			case completed := <-result:
				lastResult, lastErr = completed.state, completed.err
				cancel()
			case <-attemptCtx.Done():
				if ctx.Err() != nil {
					cancel()
					if budget.parentContext != nil && budget.parentContext.Err() != nil {
						completed := <-result
						if completed.state != nil {
							lastResult = completed.state
						}
					}
					return lastResult, runnable.executionContextError(ctx, config, budget, nodeID)
				}
				lastErr = core.NewExecutionError(core.ErrorTimeout, fmt.Sprintf("node %q timed out after %s", nodeID, policy.Timeout), attemptCtx.Err(), map[string]any{
					"node_id": nodeID,
					"timeout": policy.Timeout.String(),
				})
				timedOutAttempt = result
				cancel()
			}
		}
		if lastErr == nil {
			return lastResult, nil
		}
		var nodeInterrupt *core.NodeInterrupt
		var graphInterrupt *fruntime.GraphInterrupt
		if errors.As(lastErr, &nodeInterrupt) || errors.As(lastErr, &graphInterrupt) || !retryable(policy.Retry, lastErr) || attempt == policy.Retry.MaxAttempts {
			return lastResult, lastErr
		}
		delay := retryDelay(policy.Retry, attempt)
		if err := runnable.notifySchedulerEvent(ctx, config, fruntime.SchedulerEvent{
			Type:   fruntime.SchedulerEventRetryScheduled,
			NodeID: nodeID,
			Payload: map[string]any{
				"attempt":      attempt,
				"next_attempt": attempt + 1,
				"delay":        delay.String(),
				"error_class":  core.ClassifyError(lastErr),
				"error":        lastErr.Error(),
			},
		}); err != nil {
			return lastResult, err
		}
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return lastResult, ctx.Err()
		}
	}
	return lastResult, lastErr
}

type attemptResult struct {
	state *state.State
	err   error
}

func (runnable *scheduledRunnable) executionContextError(ctx context.Context, config fruntime.SchedulerConfig, budget *executionBudget, nodeID string) error {
	if budget != nil && budget.parentContext != nil && budget.parentContext.Err() == nil {
		limitErr := core.NewExecutionError(core.ErrorTimeout, "graph wall-time limit exceeded", ctx.Err(), map[string]any{
			"limit": budget.maxWallTime.String(),
			"kind":  "wall_time",
		})
		return runnable.notifyLimitExceeded(context.WithoutCancel(ctx), config, nodeID, limitErr)
	}
	return ctx.Err()
}

func retryable(policy fruntime.RetryPolicy, err error) bool {
	if err == nil {
		return false
	}
	class := core.ClassifyError(err)
	for _, blocked := range policy.NonRetryableErrorClasses {
		if class == blocked {
			return false
		}
	}
	for _, allowed := range policy.RetryableErrorClasses {
		if class == allowed {
			return true
		}
	}
	return false
}

func retryDelay(policy fruntime.RetryPolicy, attempt int) time.Duration {
	base := float64(policy.InitialInterval)
	if base <= 0 {
		return 0
	}
	delay := base * math.Pow(policy.BackoffMultiplier, float64(max(attempt-1, 0)))
	if maxInterval := float64(policy.MaxInterval); maxInterval > 0 && delay > maxInterval {
		delay = maxInterval
	}
	if policy.Jitter > 0 {
		factor := 1 + ((rand.Float64()*2)-1)*policy.Jitter
		delay *= factor
	}
	if delay < 0 {
		return 0
	}
	return time.Duration(delay)
}

func (budget *executionBudget) claimNodeExecution() error {
	if budget == nil || budget.maxNodeExecutions <= 0 {
		return nil
	}
	actual := budget.nodeExecutions.Add(1)
	if actual <= budget.maxNodeExecutions {
		return nil
	}
	return core.NewExecutionError(core.ErrorResourceExhausted, "graph node execution limit exceeded", nil, map[string]any{
		"limit":  budget.maxNodeExecutions,
		"actual": actual,
		"kind":   "node_executions",
	})
}

func (budget *executionBudget) snapshot() fruntime.GraphExecutionBudget {
	if budget == nil {
		return fruntime.GraphExecutionBudget{}
	}
	elapsedWallTime := budget.elapsedWallTime
	if !budget.startedAt.IsZero() {
		elapsedWallTime += time.Since(budget.startedAt)
	}
	return fruntime.GraphExecutionBudget{
		SuperSteps:      budget.superSteps.Load(),
		NodeExecutions:  budget.nodeExecutions.Load(),
		ElapsedWallTime: elapsedWallTime,
	}
}

func (runnable *scheduledRunnable) storeExecutionBudget(currentState *state.State, budget *executionBudget) error {
	if err := fruntime.StoreGraphExecutionBudget(currentState, budget.snapshot()); err != nil {
		return fmt.Errorf("store graph execution budget: %w", err)
	}
	return nil
}

func (runnable *scheduledRunnable) validateStateSize(ctx context.Context, config fruntime.SchedulerConfig, currentState *state.State) error {
	data, err := json.Marshal(currentState)
	if err != nil {
		return fmt.Errorf("measure graph state size: %w", err)
	}
	limit := runnable.graph.executionPolicy.Limits.MaxStateBytes
	if int64(len(data)) <= limit {
		return nil
	}
	limitErr := core.NewExecutionError(core.ErrorResourceExhausted, "graph state size limit exceeded", nil, map[string]any{
		"limit":  limit,
		"actual": len(data),
		"kind":   "state_bytes",
	})
	return runnable.notifyLimitExceeded(ctx, config, "", limitErr)
}

func (runnable *scheduledRunnable) notifyLimitExceeded(ctx context.Context, config fruntime.SchedulerConfig, nodeID string, limitErr error) error {
	payload := map[string]any{
		"error":       limitErr.Error(),
		"error_class": core.ClassifyError(limitErr),
	}
	var executionErr core.ExecutionError
	if errors.As(limitErr, &executionErr) {
		for key, value := range executionErr.Details() {
			payload[key] = value
		}
	}
	if err := runnable.notifySchedulerEvent(context.WithoutCancel(ctx), config, fruntime.SchedulerEvent{
		Type:    fruntime.SchedulerEventLimitExceeded,
		NodeID:  nodeID,
		Payload: payload,
	}); err != nil {
		return errors.Join(limitErr, err)
	}
	return limitErr
}

func (runnable *scheduledRunnable) notifySchedulerEvent(ctx context.Context, config fruntime.SchedulerConfig, event fruntime.SchedulerEvent) error {
	if config.EventObserver == nil {
		return nil
	}
	return config.EventObserver(ctx, event)
}

func (runnable *scheduledRunnable) acquireConcurrency(ctx context.Context, config fruntime.SchedulerConfig, limiter *core.ConcurrencyLimiter, scope, nodeID string) (func(), error) {
	if release, ok := limiter.TryAcquire(); ok {
		return release, nil
	}
	if err := runnable.notifyBackpressure(context.WithoutCancel(ctx), config, scope, nodeID, limiter.Limit()); err != nil {
		return nil, err
	}
	return limiter.Acquire(ctx)
}

func (runnable *scheduledRunnable) notifyBackpressure(ctx context.Context, config fruntime.SchedulerConfig, scope, nodeID string, limit int) error {
	return runnable.notifySchedulerEvent(ctx, config, fruntime.SchedulerEvent{
		Type:   fruntime.SchedulerEventBackpressure,
		NodeID: nodeID,
		Payload: map[string]any{
			"scope": scope,
			"limit": limit,
		},
	})
}

func (runnable *scheduledRunnable) resolveNextNodes(ctx context.Context, currentNodes []string, currentState *state.State) ([]string, error) {
	nextSet := map[string]struct{}{}
	for _, nodeID := range currentNodes {
		nextNodes, err := runnable.graph.resolveNextNodes(ctx, nodeID, currentState)
		if err != nil {
			return nil, err
		}
		for _, nextNodeID := range nextNodes {
			nextSet[nextNodeID] = struct{}{}
		}
	}
	return sortedNodeIDSet(nextSet), nil
}

func (runnable *scheduledRunnable) scheduleNext(nextNodes []string, pendingFanIn map[string]struct{}) ([]string, map[string]struct{}) {
	ready := map[string]struct{}{}
	for _, nodeID := range nextNodes {
		if runnable.isJoinNode(nodeID) {
			pendingFanIn[nodeID] = struct{}{}
			continue
		}
		ready[nodeID] = struct{}{}
	}
	runnable.releasePendingFanIn(ready, pendingFanIn)
	return sortedNodeIDSet(ready), pendingFanIn
}

func (runnable *scheduledRunnable) resumeSchedule(startNodes []string, pendingFanIn map[string]struct{}) ([]string, map[string]struct{}) {
	ready := nodeIDSet(startNodes)
	for nodeID := range ready {
		delete(pendingFanIn, nodeID)
	}
	runnable.releasePendingFanIn(ready, pendingFanIn)
	return sortedNodeIDSet(ready), pendingFanIn
}

func (runnable *scheduledRunnable) releasePendingFanIn(ready, pendingFanIn map[string]struct{}) {
	for {
		released := false
		for _, fanInNodeID := range sortedNodeIDSet(pendingFanIn) {
			if runnable.hasUpstreamBlocker(fanInNodeID, ready, pendingFanIn) {
				continue
			}
			delete(pendingFanIn, fanInNodeID)
			ready[fanInNodeID] = struct{}{}
			released = true
		}
		if !released {
			return
		}
	}
}

func (runnable *scheduledRunnable) hasUpstreamBlocker(fanInNodeID string, ready, pendingFanIn map[string]struct{}) bool {
	for nodeID := range ready {
		if runnable.isStrictUpstream(nodeID, fanInNodeID) {
			return true
		}
	}
	for nodeID := range pendingFanIn {
		if nodeID != fanInNodeID && runnable.isStrictUpstream(nodeID, fanInNodeID) {
			return true
		}
	}
	return false
}

func (runnable *scheduledRunnable) isStrictUpstream(nodeID, targetNodeID string) bool {
	if nodeID == "" || nodeID == endNodeID || nodeID == targetNodeID {
		return false
	}
	return runnable.reachability[nodeID][targetNodeID] && !runnable.reachability[targetNodeID][nodeID]
}

func (runnable *scheduledRunnable) isJoinNode(nodeID string) bool {
	_, ok := runnable.joinNodes[nodeID]
	return ok
}

func (runnable *scheduledRunnable) notifyGraphStep(ctx context.Context, config fruntime.SchedulerConfig, nodesRan []string, currentState *state.State) error {
	if config.StepObserver == nil {
		return nil
	}
	stepNodeID := ""
	if len(nodesRan) == 1 {
		stepNodeID = nodesRan[0]
	} else if len(nodesRan) > 1 {
		stepNodeID = fmt.Sprintf("step:%v", nodesRan)
	}
	if err := config.StepObserver(ctx, stepNodeID, currentState); err != nil {
		return &fruntime.GraphStepError{Err: err}
	}
	return nil
}

func (g *Graph) compileJoinNodes() map[string]struct{} {
	incoming := map[string]map[string]struct{}{}
	for fromNodeID, targets := range g.defaultEdges {
		for _, targetNodeID := range targets {
			if targetNodeID == endNodeID {
				continue
			}
			if incoming[targetNodeID] == nil {
				incoming[targetNodeID] = map[string]struct{}{}
			}
			incoming[targetNodeID][fromNodeID] = struct{}{}
		}
	}
	for fromNodeID, edges := range g.conditionalEdges {
		for _, edge := range edges {
			if edge.to == endNodeID {
				continue
			}
			if incoming[edge.to] == nil {
				incoming[edge.to] = map[string]struct{}{}
			}
			incoming[edge.to][fromNodeID] = struct{}{}
		}
	}
	joins := map[string]struct{}{}
	for nodeID, predecessors := range incoming {
		if len(predecessors) > 1 {
			joins[nodeID] = struct{}{}
		}
	}
	return joins
}

func (g *Graph) compileReachability() map[string]map[string]bool {
	reachability := make(map[string]map[string]bool, len(g.nodes))
	for nodeID := range g.nodes {
		reachable := map[string]bool{}
		queue := []string{nodeID}
		for len(queue) > 0 {
			currentNodeID := queue[0]
			queue = queue[1:]
			for _, targetNodeID := range g.outgoingNodeIDs(currentNodeID) {
				if targetNodeID == endNodeID || reachable[targetNodeID] {
					continue
				}
				reachable[targetNodeID] = true
				queue = append(queue, targetNodeID)
			}
		}
		reachability[nodeID] = reachable
	}
	return reachability
}

func (g *Graph) outgoingNodeIDs(nodeID string) []string {
	targets := append([]string(nil), g.defaultEdges[nodeID]...)
	for _, edge := range g.conditionalEdges[nodeID] {
		targets = append(targets, edge.to)
	}
	return targets
}

func firstNodeInterrupt(nodeIDs []string, nodeErrors []error) *core.NodeInterrupt {
	for index, nodeErr := range nodeErrors {
		var interrupt *core.NodeInterrupt
		if errors.As(nodeErr, &interrupt) {
			if interrupt.NodeID == "" && index < len(nodeIDs) {
				interrupt.NodeID = nodeIDs[index]
			}
			return interrupt
		}
	}
	return nil
}

func firstError(errorsList []error) error {
	for _, err := range errorsList {
		if err != nil {
			return err
		}
	}
	return nil
}

func configuredInterruptNode(nodeIDs []string, config fruntime.SchedulerConfig, before bool) string {
	if before {
		return ""
	}
	for _, nodeID := range nodeIDs {
		for _, configuredNodeID := range config.InterruptAfterNodeIDs {
			if nodeID == configuredNodeID {
				return nodeID
			}
		}
	}
	return ""
}

func activeNodeIDs(nodeIDs []string) []string {
	active := map[string]struct{}{}
	for _, nodeID := range nodeIDs {
		if nodeID != "" && nodeID != endNodeID {
			active[nodeID] = struct{}{}
		}
	}
	return sortedNodeIDSet(active)
}

func nodeIDSet(nodeIDs []string) map[string]struct{} {
	result := make(map[string]struct{}, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		if nodeID != "" {
			result[nodeID] = struct{}{}
		}
	}
	return result
}

func sortedNodeIDSet(nodeIDs map[string]struct{}) []string {
	result := make([]string, 0, len(nodeIDs))
	for nodeID := range nodeIDs {
		result = append(result, nodeID)
	}
	sort.Strings(result)
	return result
}
