package graph

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
	langgraph "github.com/smallnest/langgraphgo/graph"
)

type scheduledNodeExecutor func(context.Context, string, *state.State) (*state.State, error)

const maxConfiguredRetries = 100

type scheduledRunnable struct {
	graph        *Graph
	patches      *compilePatchCollector
	executeNode  scheduledNodeExecutor
	observeNode  func(context.Context, langgraph.NodeEvent, string, *state.State, error, time.Duration)
	tracer       *langgraph.Tracer
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
	return runnable.InvokeWithConfig(ctx, initialState, nil)
}

func (runnable *scheduledRunnable) InvokeWithConfig(ctx context.Context, initialState *state.State, config *langgraph.Config) (*state.State, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if runnable != nil && runnable.tracer != nil {
		graphSpan := runnable.tracer.StartSpan(ctx, langgraph.TraceEventGraphStart, "graph")
		graphSpan.State = initialState
		finalState, err := runnable.invokeWithConfig(ctx, initialState, config)
		runnable.tracer.EndSpan(ctx, graphSpan, finalState, err)
		return finalState, err
	}
	return runnable.invokeWithConfig(ctx, initialState, config)
}

func (runnable *scheduledRunnable) invokeWithConfig(ctx context.Context, initialState *state.State, config *langgraph.Config) (*state.State, error) {
	if runnable == nil || runnable.graph == nil {
		return nil, fmt.Errorf("scheduled graph is nil")
	}
	if runnable.executeNode == nil {
		return nil, fmt.Errorf("scheduled graph node executor is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if config != nil {
		ctx = langgraph.WithConfig(ctx, config)
		if config.ResumeValue != nil {
			ctx = langgraph.WithResumeValue(ctx, config.ResumeValue)
		}
	}

	currentState := state.NewState()
	if initialState != nil {
		currentState = initialState.Clone()
	}
	currentNodes := []string{runnable.graph.entryPoint}
	if config != nil && len(config.ResumeFrom) > 0 {
		currentNodes = append([]string(nil), config.ResumeFrom...)
	}
	_, restoredPending, _ := fruntime.LoadGraphSchedule(currentState)
	pendingFanIn := nodeIDSet(restoredPending)
	currentNodes, pendingFanIn = runnable.resumeSchedule(currentNodes, pendingFanIn)

	runID := fmt.Sprintf("graph-%d", time.Now().UnixNano())
	runnable.notifyChainStart(ctx, config, runID, currentState)

	for {
		currentNodes = activeNodeIDs(currentNodes)
		if len(currentNodes) == 0 {
			break
		}
		if interruptedNode := configuredInterruptNode(currentNodes, config, true); interruptedNode != "" {
			return currentState, &langgraph.GraphInterrupt{Node: interruptedNode, State: currentState}
		}
		if err := fruntime.StoreGraphSchedule(currentState, nil, sortedNodeIDSet(pendingFanIn)); err != nil {
			return currentState, err
		}

		results, nodeErrors := runnable.executeNodes(ctx, currentNodes, currentState)
		mergedState, err := runnable.graph.mergeCompiledStates(ctx, currentState, results, runnable.patches)
		if err != nil {
			mergeErr := fmt.Errorf("state merge failed: %w", err)
			runnable.notifyChainError(ctx, config, runID, mergeErr)
			return currentState, mergeErr
		}
		currentState = mergedState

		if interrupt := firstNodeInterrupt(currentNodes, nodeErrors); interrupt != nil {
			if err := runnable.notifyGraphStep(ctx, config, currentNodes, currentState); err != nil {
				runnable.notifyChainError(ctx, config, runID, err)
				return currentState, err
			}
			return currentState, &langgraph.GraphInterrupt{
				Node:           interrupt.Node,
				State:          currentState,
				InterruptValue: interrupt.Value,
				NextNodes:      []string{interrupt.Node},
			}
		}
		if nodeErr := firstError(nodeErrors); nodeErr != nil {
			runnable.notifyChainError(ctx, config, runID, nodeErr)
			return currentState, nodeErr
		}

		nodesRan := append([]string(nil), currentNodes...)
		nextNodes, err := runnable.resolveNextNodes(ctx, nodesRan, currentState)
		if err != nil {
			runnable.notifyChainError(ctx, config, runID, err)
			return currentState, err
		}
		currentNodes, pendingFanIn = runnable.scheduleNext(nextNodes, pendingFanIn)
		if err := fruntime.StoreGraphSchedule(currentState, currentNodes, sortedNodeIDSet(pendingFanIn)); err != nil {
			return currentState, err
		}
		if err := runnable.notifyGraphStep(ctx, config, nodesRan, currentState); err != nil {
			runnable.notifyChainError(ctx, config, runID, err)
			return currentState, err
		}

		if interruptedNode := configuredInterruptNode(nodesRan, config, false); interruptedNode != "" {
			return currentState, &langgraph.GraphInterrupt{
				Node:      interruptedNode,
				State:     currentState,
				NextNodes: append([]string(nil), currentNodes...),
			}
		}
	}

	if err := fruntime.ClearGraphSchedule(currentState); err != nil {
		return currentState, err
	}
	runnable.notifyChainEnd(ctx, config, runID, currentState)
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
			var nodeSpan *langgraph.TraceSpan
			if runnable.tracer != nil {
				nodeSpan = runnable.tracer.StartSpan(ctx, langgraph.TraceEventNodeStart, targetNodeID)
				nodeSpan.State = currentState
			}
			runnable.notifyNode(ctx, langgraph.NodeEventStart, targetNodeID, currentState, nil, 0)
			defer func() {
				if recovered := recover(); recovered != nil {
					results[resultIndex] = currentState
					nodeErrors[resultIndex] = fmt.Errorf("panic in node %s: %v", targetNodeID, recovered)
					runnable.notifyNode(ctx, langgraph.NodeEventError, targetNodeID, currentState, nodeErrors[resultIndex], time.Since(startedAt))
					if nodeSpan != nil {
						runnable.tracer.EndSpan(ctx, nodeSpan, currentState, nodeErrors[resultIndex])
					}
				}
			}()
			result, err := runnable.executeNodeWithRetry(ctx, targetNodeID, currentState)
			if result == nil {
				result = currentState
			}
			results[resultIndex] = result
			if err != nil {
				nodeErrors[resultIndex] = fmt.Errorf("error in node %s: %w", targetNodeID, err)
				runnable.notifyNode(ctx, langgraph.NodeEventError, targetNodeID, result, nodeErrors[resultIndex], time.Since(startedAt))
				if nodeSpan != nil {
					runnable.tracer.EndSpan(ctx, nodeSpan, result, nodeErrors[resultIndex])
				}
				return
			}
			runnable.notifyNode(ctx, langgraph.NodeEventComplete, targetNodeID, result, nil, time.Since(startedAt))
			if nodeSpan != nil {
				runnable.tracer.EndSpan(ctx, nodeSpan, result, nil)
			}
		}()
	}
	waitGroup.Wait()
	return results, nodeErrors
}

func (runnable *scheduledRunnable) notifyNode(ctx context.Context, event langgraph.NodeEvent, nodeID string, currentState *state.State, err error, duration time.Duration) {
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
		var nodeInterrupt *langgraph.NodeInterrupt
		var graphInterrupt *langgraph.GraphInterrupt
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

func retryDelay(strategy langgraph.BackoffStrategy, attempt int) time.Duration {
	const maxRetryDelay = 30 * time.Second
	if attempt < 0 {
		return time.Second
	}
	switch strategy {
	case langgraph.ExponentialBackoff:
		if attempt >= 5 {
			return maxRetryDelay
		}
		return time.Second * time.Duration(1<<uint(attempt))
	case langgraph.LinearBackoff:
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
	if nodeID == "" || nodeID == langgraph.END || nodeID == targetNodeID {
		return false
	}
	return runnable.reachability[nodeID][targetNodeID] && !runnable.reachability[targetNodeID][nodeID]
}

func (runnable *scheduledRunnable) isJoinNode(nodeID string) bool {
	_, ok := runnable.joinNodes[nodeID]
	return ok
}

func (runnable *scheduledRunnable) notifyGraphStep(ctx context.Context, config *langgraph.Config, nodesRan []string, currentState *state.State) error {
	if config == nil {
		return nil
	}
	stepNodeID := ""
	if len(nodesRan) == 1 {
		stepNodeID = nodesRan[0]
	} else if len(nodesRan) > 1 {
		stepNodeID = fmt.Sprintf("step:%v", nodesRan)
	}
	for _, callback := range config.Callbacks {
		if graphStepCallback, ok := callback.(interface {
			OnGraphStepWithError(context.Context, string, any) error
		}); ok {
			if err := graphStepCallback.OnGraphStepWithError(ctx, stepNodeID, currentState); err != nil {
				return err
			}
			continue
		}
		if graphCallback, ok := callback.(langgraph.GraphCallbackHandler); ok {
			graphCallback.OnGraphStep(ctx, stepNodeID, currentState)
		}
	}
	return nil
}

func (runnable *scheduledRunnable) notifyChainStart(ctx context.Context, config *langgraph.Config, runID string, currentState *state.State) {
	if config == nil {
		return
	}
	serialized := map[string]any{"name": "graph", "type": "chain"}
	for _, callback := range config.Callbacks {
		callback.OnChainStart(ctx, serialized, currentState.Export(), runID, nil, config.Tags, config.Metadata)
	}
}

func (runnable *scheduledRunnable) notifyChainEnd(ctx context.Context, config *langgraph.Config, runID string, currentState *state.State) {
	if config == nil {
		return
	}
	for _, callback := range config.Callbacks {
		callback.OnChainEnd(ctx, currentState.Export(), runID)
	}
}

func (runnable *scheduledRunnable) notifyChainError(ctx context.Context, config *langgraph.Config, runID string, err error) {
	if config == nil {
		return
	}
	for _, callback := range config.Callbacks {
		callback.OnChainError(ctx, err, runID)
	}
}

func (g *Graph) compileJoinNodes() map[string]struct{} {
	incoming := map[string]map[string]struct{}{}
	for fromNodeID, targets := range g.defaultEdges {
		for _, targetNodeID := range targets {
			if targetNodeID == langgraph.END {
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
			if edge.to == langgraph.END {
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
				if targetNodeID == langgraph.END || reachable[targetNodeID] {
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

func firstNodeInterrupt(nodeIDs []string, nodeErrors []error) *langgraph.NodeInterrupt {
	for index, nodeErr := range nodeErrors {
		var interrupt *langgraph.NodeInterrupt
		if errors.As(nodeErr, &interrupt) {
			if interrupt.Node == "" && index < len(nodeIDs) {
				interrupt.Node = nodeIDs[index]
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

func configuredInterruptNode(nodeIDs []string, config *langgraph.Config, before bool) string {
	if config == nil {
		return ""
	}
	configured := config.InterruptAfter
	if before {
		configured = config.InterruptBefore
	}
	for _, nodeID := range nodeIDs {
		for _, configuredNodeID := range configured {
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
		if nodeID != "" && nodeID != langgraph.END {
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
