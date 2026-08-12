package graph

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dengzii/weaveflow/core"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

type scheduledNodeExecutor func(context.Context, string, *state.State) (*state.State, error)

const maxConfiguredRetries = 100

type scheduledRunnable struct {
	graph        *Graph
	patches      *compilePatchCollector
	executeNode  scheduledNodeExecutor
	observeNode  func(context.Context, NodeEvent, string, *state.State, error, time.Duration)
	joinNodes    map[string]struct{}
	reachability map[string]map[string]bool
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
	currentState := state.NewState()
	if initialState != nil {
		currentState = initialState.Clone()
	}
	currentNodes := []string{runnable.graph.entryPoint}
	if len(config.StartNodeIDs) > 0 {
		currentNodes = append([]string(nil), config.StartNodeIDs...)
	}
	_, restoredPending, _ := fruntime.LoadGraphSchedule(currentState)
	pendingFanIn := nodeIDSet(restoredPending)
	currentNodes, pendingFanIn = runnable.resumeSchedule(currentNodes, pendingFanIn)

	for {
		currentNodes = activeNodeIDs(currentNodes)
		if len(currentNodes) == 0 {
			break
		}
		if err := fruntime.StoreGraphSchedule(currentState, nil, sortedNodeIDSet(pendingFanIn)); err != nil {
			return currentState, err
		}

		results, nodeErrors := runnable.executeNodes(ctx, currentNodes, currentState)
		mergedState, err := runnable.graph.mergeCompiledStates(ctx, currentState, results, runnable.patches)
		if err != nil {
			mergeErr := fmt.Errorf("state merge failed: %w", err)
			return currentState, mergeErr
		}
		currentState = mergedState

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

func (runnable *scheduledRunnable) executeNodes(ctx context.Context, nodeIDs []string, currentState *state.State) ([]*state.State, []error) {
	results := make([]*state.State, len(nodeIDs))
	nodeErrors := make([]error, len(nodeIDs))
	var waitGroup sync.WaitGroup
	waitGroup.Add(len(nodeIDs))
	for index, nodeID := range nodeIDs {
		resultIndex := index
		targetNodeID := nodeID
		go func() {
			defer waitGroup.Done()
			startedAt := time.Now()
			runnable.notifyNode(ctx, EventNodeStart, targetNodeID, currentState, nil, 0)
			defer func() {
				if recovered := recover(); recovered != nil {
					results[resultIndex] = currentState
					nodeErrors[resultIndex] = fmt.Errorf("panic in node %s: %v", targetNodeID, recovered)
					runnable.notifyNode(ctx, EventNodeError, targetNodeID, currentState, nodeErrors[resultIndex], time.Since(startedAt))
				}
			}()
			result, err := runnable.executeNodeWithRetry(ctx, targetNodeID, currentState)
			if result == nil {
				result = currentState
			}
			results[resultIndex] = result
			if err != nil {
				nodeErrors[resultIndex] = fmt.Errorf("error in node %s: %w", targetNodeID, err)
				runnable.notifyNode(ctx, EventNodeError, targetNodeID, result, nodeErrors[resultIndex], time.Since(startedAt))
				return
			}
			runnable.notifyNode(ctx, EventNodeComplete, targetNodeID, result, nil, time.Since(startedAt))
		}()
	}
	waitGroup.Wait()
	return results, nodeErrors
}

func (runnable *scheduledRunnable) notifyNode(ctx context.Context, event NodeEvent, nodeID string, currentState *state.State, err error, duration time.Duration) {
	if runnable.observeNode != nil {
		runnable.observeNode(ctx, event, nodeID, currentState, err, duration)
	}
}

func (runnable *scheduledRunnable) executeNodeWithRetry(ctx context.Context, nodeID string, currentState *state.State) (*state.State, error) {
	maxAttempts := 1
	if runnable.graph.retryPolicy != nil {
		maxAttempts += runnable.graph.retryPolicy.MaxRetries
	}
	var lastResult *state.State
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		lastResult, lastErr = runnable.executeNode(ctx, nodeID, currentState)
		if lastErr == nil {
			return lastResult, nil
		}
		var nodeInterrupt *core.NodeInterrupt
		var graphInterrupt *fruntime.GraphInterrupt
		if errors.As(lastErr, &nodeInterrupt) || errors.As(lastErr, &graphInterrupt) || !runnable.retryable(lastErr) || attempt == maxAttempts-1 {
			return lastResult, lastErr
		}
		delay := retryDelay(runnable.graph.retryPolicy.BackoffStrategy, attempt)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return lastResult, ctx.Err()
		}
	}
	return lastResult, lastErr
}

func (runnable *scheduledRunnable) retryable(err error) bool {
	if runnable.graph.retryPolicy == nil || err == nil {
		return false
	}
	for _, pattern := range runnable.graph.retryPolicy.RetryableErrors {
		if strings.TrimSpace(pattern) == "" {
			continue
		}
		if strings.Contains(err.Error(), pattern) {
			return true
		}
	}
	return false
}

func retryDelay(strategy BackoffStrategy, attempt int) time.Duration {
	const maxRetryDelay = 30 * time.Second
	if attempt < 0 {
		return time.Second
	}
	switch strategy {
	case ExponentialBackoff:
		if attempt >= 5 {
			return maxRetryDelay
		}
		return time.Second * time.Duration(1<<uint(attempt))
	case LinearBackoff:
		delay := time.Second * time.Duration(attempt+1)
		if delay > maxRetryDelay {
			return maxRetryDelay
		}
		return delay
	default:
		return time.Second
	}
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
