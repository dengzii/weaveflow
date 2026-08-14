package graph

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/registry"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

type scheduledNodeExecutor func(context.Context, fruntime.GraphTask, *state.State) (core.ExecutionResult, error)
type scheduledNodePreparer func(context.Context, fruntime.GraphTask, *state.State) (context.Context, error)

type scheduledRunnable struct {
	graph         *Graph
	patches       *compilePatchCollector
	prepareNode   scheduledNodePreparer
	executeNode   scheduledNodeExecutor
	observeNode   func(context.Context, NodeEvent, string, *state.State, error, time.Duration)
	recordFailure func(context.Context, fruntime.GraphTask, error, []fruntime.GraphTask) error
	recordTaskErr func(fruntime.GraphTask, error)
	joinNodes     map[string]struct{}
	reachability  map[string]map[string]bool
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
	currentTasks := []fruntime.GraphTask{fruntime.NewStaticGraphTask(runnable.graph.entryPoint, 0)}
	if len(config.StartTasks) > 0 {
		currentTasks = fruntime.CloneGraphTasks(config.StartTasks)
	}
	restoredSchedule, _ := fruntime.LoadGraphSchedule(currentState)
	pendingFanIn := nodeIDSet(restoredSchedule.PendingFanInNodes)
	currentTasks, pendingFanIn = runnable.resumeSchedule(currentTasks, pendingFanIn)
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
	nextWave:
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
		currentTasks, err = runnable.activeTasks(currentTasks)
		if err != nil {
			return currentState, err
		}
		if len(currentTasks) == 0 {
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
		if len(currentTasks) > policy.Limits.MaxFanOut {
			limitErr := core.NewExecutionError(core.ErrorResourceExhausted, "graph fan-out limit exceeded", nil, map[string]any{
				"limit":  policy.Limits.MaxFanOut,
				"actual": len(currentTasks),
				"kind":   "fan_out",
			})
			return currentState, runnable.notifyLimitExceeded(ctx, config, "", limitErr)
		}
		if err := fruntime.StoreGraphSchedule(currentState, fruntime.GraphSchedule{
			CurrentTasks:      currentTasks,
			PendingFanInNodes: sortedNodeIDSet(pendingFanIn),
		}); err != nil {
			return currentState, err
		}
		if err := runnable.storeExecutionBudget(currentState, budget); err != nil {
			return currentState, err
		}

		completedTasks := fruntime.CloneGraphTasks(currentTasks)
		results, nodeErrors := runnable.executeTasks(ctx, config, budget, completedTasks, currentState)
		if interrupt, task := firstNodeInterrupt(completedTasks, nodeErrors); interrupt != nil && !hasNonInterruptError(nodeErrors) {
			mergedState, err := runnable.mergeWaveResults(ctx, config, budget, currentState, results)
			if err != nil {
				return currentState, err
			}
			currentState = mergedState
			if err := runnable.notifyGraphStep(ctx, config, completedTasks, currentState); err != nil {
				return currentState, err
			}
			return currentState, &fruntime.GraphInterrupt{
				NodeID:    interrupt.NodeID,
				TaskID:    task.TaskID,
				State:     currentState,
				Value:     interrupt.Value,
				NextTasks: []fruntime.GraphTask{task},
			}
		}
		if nodeErr := firstError(nodeErrors); nodeErr != nil {
			if errorCount(nodeErrors) > 1 {
				failedState, stateErr := runnable.failedWaveState(currentState, budget)
				return failedState, errors.Join(errors.Join(nodeErrors...), stateErr)
			}
			for index, task := range completedTasks {
				if nodeErrors[index] == nil {
					continue
				}
				failureTasks, resolveErr := runnable.graph.resolveFailure(ctx, task, string(dsl.FailureStageNode), nodeErrors[index])
				if resolveErr != nil {
					failedState, stateErr := runnable.failedWaveState(currentState, budget)
					return failedState, errors.Join(resolveErr, stateErr)
				}
				if len(failureTasks) > 0 {
					mergedState, mergeErr := runnable.mergeWaveResults(ctx, config, budget, currentState, results)
					if mergeErr != nil {
						return currentState, mergeErr
					}
					nextTasks, returnCommand, suspend, commandErr := runnable.resolveCommands(ctx, config, completedTasks, results, nodeErrors, mergedState)
					if commandErr != nil {
						if event := conditionSchedulerEvent(commandErr); event != nil {
							if notifyErr := runnable.notifySchedulerEvent(context.WithoutCancel(ctx), config, *event); notifyErr != nil {
								commandErr = errors.Join(commandErr, notifyErr)
							}
						}
						return currentState, errors.Join(nodeErr, commandErr)
					}
					if returnCommand != nil || suspend != nil {
						return currentState, fmt.Errorf("failure-routed wave resolved an incompatible control command")
					}
					if runnable.recordFailure != nil {
						if err := runnable.recordFailure(context.WithoutCancel(ctx), task, nodeErrors[index], failureTasks); err != nil {
							return currentState, err
						}
					} else if err := runnable.notifySchedulerEvent(context.WithoutCancel(ctx), config, failureRoutedSchedulerEvent(task, nodeErrors[index], failureTasks)); err != nil {
						return currentState, err
					}
					currentState = mergedState
					nextTasks = append(nextTasks, failureTasks...)
					currentTasks, pendingFanIn = runnable.scheduleNext(nextTasks, pendingFanIn)
					if err := fruntime.StoreGraphSchedule(currentState, fruntime.GraphSchedule{NextTasks: currentTasks, PendingFanInNodes: sortedNodeIDSet(pendingFanIn)}); err != nil {
						return currentState, err
					}
					if err := runnable.notifyGraphStep(ctx, config, completedTasks, currentState); err != nil {
						return currentState, err
					}
					goto nextWave
				}
			}
			failedState, stateErr := runnable.failedWaveState(currentState, budget)
			return failedState, errors.Join(nodeErr, stateErr)
		}

		mergedState, err := runnable.mergeWaveResults(ctx, config, budget, currentState, results)
		if err != nil {
			return currentState, err
		}
		currentState = mergedState

		nextTasks, returnCommand, suspend, err := runnable.resolveCommands(ctx, config, completedTasks, results, nodeErrors, currentState)
		if err != nil {
			if event := conditionSchedulerEvent(err); event != nil {
				if notifyErr := runnable.notifySchedulerEvent(context.WithoutCancel(ctx), config, *event); notifyErr != nil {
					return currentState, errors.Join(err, notifyErr)
				}
			}
			failureTask := completedTasks[0]
			var conditionErr *ConditionError
			if errors.As(err, &conditionErr) {
				for _, completedTask := range completedTasks {
					if completedTask.NodeID == conditionErr.SourceNodeID {
						failureTask = completedTask
						break
					}
				}
			}
			failureTasks, resolveErr := runnable.graph.resolveFailure(ctx, failureTask, string(dsl.FailureStageCondition), err)
			if resolveErr != nil {
				return currentState, resolveErr
			}
			if len(failureTasks) > 0 {
				if runnable.recordFailure != nil {
					if recordErr := runnable.recordFailure(context.WithoutCancel(ctx), failureTask, err, failureTasks); recordErr != nil {
						return currentState, recordErr
					}
				} else if notifyErr := runnable.notifySchedulerEvent(context.WithoutCancel(ctx), config, failureRoutedSchedulerEvent(failureTask, err, failureTasks)); notifyErr != nil {
					return currentState, notifyErr
				}
				currentTasks, pendingFanIn = runnable.scheduleNext(failureTasks, pendingFanIn)
				if storeErr := fruntime.StoreGraphSchedule(currentState, fruntime.GraphSchedule{NextTasks: currentTasks, PendingFanInNodes: sortedNodeIDSet(pendingFanIn)}); storeErr != nil {
					return currentState, storeErr
				}
				if stepErr := runnable.notifyGraphStep(ctx, config, completedTasks, currentState); stepErr != nil {
					return currentState, stepErr
				}
				goto nextWave
			}
			return currentState, err
		}
		if returnCommand != nil {
			if err := fruntime.StoreGraphReturnValue(currentState, returnCommand.Value); err != nil {
				return currentState, fmt.Errorf("store graph return value: %w", err)
			}
			if err := runnable.notifyGraphStep(ctx, config, completedTasks, currentState); err != nil {
				return currentState, err
			}
			break
		}
		currentTasks, pendingFanIn = runnable.scheduleNext(nextTasks, pendingFanIn)
		if err := fruntime.StoreGraphSchedule(currentState, fruntime.GraphSchedule{
			NextTasks:         currentTasks,
			PendingFanInNodes: sortedNodeIDSet(pendingFanIn),
		}); err != nil {
			return currentState, err
		}
		if err := runnable.notifyGraphStep(ctx, config, completedTasks, currentState); err != nil {
			return currentState, err
		}
		if suspend != nil {
			return currentState, &fruntime.GraphInterrupt{
				NodeID:          suspend.task.NodeID,
				TaskID:          suspend.task.TaskID,
				State:           currentState,
				Value:           suspend.request.Value,
				NextTasks:       fruntime.CloneGraphTasks(currentTasks),
				CheckpointStage: fruntime.CheckpointAfterWave,
			}
		}

		if interruptedTask, ok := configuredInterruptTask(completedTasks, config); ok {
			return currentState, &fruntime.GraphInterrupt{
				NodeID:    interruptedTask.NodeID,
				TaskID:    interruptedTask.TaskID,
				State:     currentState,
				NextTasks: fruntime.CloneGraphTasks(currentTasks),
			}
		}
	}

	if err := fruntime.ClearGraphSchedule(currentState); err != nil {
		return currentState, err
	}
	return currentState, nil
}

func (runnable *scheduledRunnable) executeTasks(ctx context.Context, config fruntime.SchedulerConfig, budget *executionBudget, graphTasks []fruntime.GraphTask, currentState *state.State) ([]core.ExecutionResult, []error) {
	results := make([]core.ExecutionResult, len(graphTasks))
	taskErrors := make([]error, len(graphTasks))
	type indexedTask struct {
		index int
		task  fruntime.GraphTask
	}
	tasks := make(chan indexedTask, len(graphTasks))
	var waitGroup sync.WaitGroup
	workerCount := min(len(graphTasks), runnable.graph.executionPolicy.Limits.MaxConcurrentNodes)
	waitGroup.Add(workerCount)
	for range workerCount {
		go func() {
			defer waitGroup.Done()
			for indexed := range tasks {
				func() {
					startedAt := time.Now()
					task := indexed.task
					runnable.notifyNode(ctx, EventNodeStart, task.NodeID, currentState, nil, 0)
					defer func() {
						if recovered := recover(); recovered != nil {
							taskErrors[indexed.index] = fmt.Errorf("panic in task %s at node %s: %v", task.TaskID, task.NodeID, recovered)
							recordFailedBranchPatch(runnable.patches, currentState, task, taskErrors[indexed.index])
							if runnable.recordTaskErr != nil {
								runnable.recordTaskErr(task, taskErrors[indexed.index])
							}
							runnable.notifyNode(ctx, EventNodeError, task.NodeID, currentState, taskErrors[indexed.index], time.Since(startedAt))
						}
					}()
					result, err := runnable.executeTaskWithRetry(ctx, config, budget, task, currentState)
					results[indexed.index] = result
					if err != nil {
						taskErrors[indexed.index] = fmt.Errorf("error in task %s at node %s: %w", task.TaskID, task.NodeID, err)
						recordFailedBranchPatch(runnable.patches, currentState, task, taskErrors[indexed.index])
						if runnable.recordTaskErr != nil {
							runnable.recordTaskErr(task, taskErrors[indexed.index])
						}
						runnable.notifyNode(ctx, EventNodeError, task.NodeID, result.State, taskErrors[indexed.index], time.Since(startedAt))
						return
					}
					runnable.notifyNode(ctx, EventNodeComplete, task.NodeID, result.State, nil, time.Since(startedAt))
				}()
			}
		}()
	}
	for index, task := range graphTasks {
		tasks <- indexedTask{index: index, task: task}
	}
	close(tasks)
	waitGroup.Wait()
	return results, taskErrors
}

func (runnable *scheduledRunnable) mergeWaveResults(ctx context.Context, config fruntime.SchedulerConfig, budget *executionBudget, currentState *state.State, results []core.ExecutionResult) (*state.State, error) {
	mergedState, err := runnable.graph.mergeCompiledResults(ctx, currentState, results, runnable.patches)
	if err != nil {
		return currentState, fmt.Errorf("state merge failed: %w", err)
	}
	if err := runnable.storeExecutionBudget(mergedState, budget); err != nil {
		return currentState, err
	}
	if err := runnable.validateStateSize(ctx, config, mergedState); err != nil {
		return currentState, err
	}
	return mergedState, nil
}

func (runnable *scheduledRunnable) failedWaveState(currentState *state.State, budget *executionBudget) (*state.State, error) {
	runnable.patches.discard(currentState)
	failedState := currentState.Clone()
	if err := runnable.storeExecutionBudget(failedState, budget); err != nil {
		return currentState, err
	}
	return failedState, nil
}

func (runnable *scheduledRunnable) notifyNode(ctx context.Context, event NodeEvent, nodeID string, currentState *state.State, err error, duration time.Duration) {
	if runnable.observeNode != nil {
		runnable.observeNode(ctx, event, nodeID, currentState, err, duration)
	}
}

func (runnable *scheduledRunnable) executeTaskWithRetry(ctx context.Context, config fruntime.SchedulerConfig, budget *executionBudget, task fruntime.GraphTask, currentState *state.State) (core.ExecutionResult, error) {
	policy := runnable.graph.nodeExecutionPolicy(task.NodeID)
	var lastResult core.ExecutionResult
	var lastErr error
	for attempt := 1; attempt <= policy.Retry.MaxAttempts; attempt++ {
		lastErr = nil
		executionCtx := fruntime.WithGraphExecutionBudgetProvider(ctx, budget.snapshot)
		nodeAttempt := fruntime.NewNodeAttempt()
		executionCtx = fruntime.WithNodeAttempt(executionCtx, nodeAttempt)
		if runnable.prepareNode != nil {
			var err error
			executionCtx, err = runnable.prepareNode(executionCtx, task, currentState)
			if err != nil {
				lastErr = err
			}
		}
		if lastErr == nil {
			if err := budget.claimNodeExecution(); err != nil {
				return lastResult, runnable.notifyLimitExceeded(ctx, config, task.NodeID, err)
			}
			releaseGraphNode, err := runnable.acquireConcurrency(executionCtx, config, runnable.graph.nodeLimiter, "graph_node", task.NodeID)
			if err != nil {
				return lastResult, err
			}
			releaseNode, err := runnable.acquireConcurrency(executionCtx, config, runnable.graph.nodeLimiters[task.NodeID], "node", task.NodeID)
			if err != nil {
				releaseGraphNode()
				return lastResult, err
			}
			attemptCtx, cancel := context.WithTimeout(executionCtx, policy.Timeout)
			result := make(chan attemptResult, 1)
			go func() {
				completed := attemptResult{}
				defer func() {
					if recovered := recover(); recovered != nil {
						completed.err = fmt.Errorf("panic in node %s: %v", task.NodeID, recovered)
					}
					nodeAttempt.TryAccept()
					releaseNode()
					releaseGraphNode()
					result <- completed
					if nodeAttempt.IsAbandoned() {
						runnable.patches.releaseAbandonedTask(currentState, task.TaskID)
					}
				}()
				completed.result, completed.err = runnable.executeNode(attemptCtx, task, currentState)
			}()
			select {
			case completed := <-result:
				lastResult, lastErr = completed.result, completed.err
				cancel()
			case <-attemptCtx.Done():
				if !nodeAttempt.TryAbandon() {
					completed := <-result
					lastResult, lastErr = completed.result, completed.err
					cancel()
					break
				}
				runnable.patches.abandonTask(currentState, task)
				cancel()
				if ctx.Err() != nil {
					return lastResult, runnable.executionContextError(ctx, config, budget, task.NodeID)
				}
				lastErr = core.NewExecutionError(core.ErrorTimeout, fmt.Sprintf("task %q at node %q timed out after %s", task.TaskID, task.NodeID, policy.Timeout), attemptCtx.Err(), map[string]any{
					"task_id":           task.TaskID,
					"node_id":           task.NodeID,
					"timeout":           policy.Timeout.String(),
					"attempt":           attempt,
					"attempt_abandoned": true,
				})
				return lastResult, lastErr
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
			NodeID: task.NodeID,
			Payload: map[string]any{
				"task_id":      task.TaskID,
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
	result core.ExecutionResult
	err    error
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
	var executionErr core.ExecutionError
	if errors.As(err, &executionErr) {
		abandoned, _ := executionErr.Details()["attempt_abandoned"].(bool)
		if abandoned {
			return false
		}
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

type taskSuspend struct {
	task    fruntime.GraphTask
	request *core.SuspendRequest
}

func (runnable *scheduledRunnable) resolveCommands(ctx context.Context, config fruntime.SchedulerConfig, completedTasks []fruntime.GraphTask, results []core.ExecutionResult, taskErrors []error, currentState *state.State) ([]fruntime.GraphTask, *core.ReturnCommand, *taskSuspend, error) {
	if len(completedTasks) != len(results) {
		return nil, nil, nil, fmt.Errorf("resolve commands: %d tasks have %d results", len(completedTasks), len(results))
	}
	if len(taskErrors) != len(completedTasks) {
		return nil, nil, nil, fmt.Errorf("resolve commands: %d tasks have %d errors", len(completedTasks), len(taskErrors))
	}
	staticTargets := map[string]struct{}{}
	dynamicTasks := make([]fruntime.GraphTask, 0)
	var suspend *taskSuspend
	var returnCommand *core.ReturnCommand
	for index, task := range completedTasks {
		if taskErrors[index] != nil {
			continue
		}
		command := results[index].Node.Command
		controlCount := 0
		if len(command.Goto) > 0 {
			controlCount++
		}
		if len(command.Send) > 0 {
			controlCount++
		}
		if command.Suspend != nil {
			controlCount++
		}
		if command.Return != nil {
			controlCount++
		}
		if controlCount > 1 {
			return nil, nil, nil, fmt.Errorf("task %q at node %q returned conflicting control commands", task.TaskID, task.NodeID)
		}
		switch {
		case command.Return != nil:
			if len(completedTasks) != 1 {
				return nil, nil, nil, fmt.Errorf("task %q cannot return from a parallel wave", task.TaskID)
			}
			commandCopy := *command.Return
			returnCommand = &commandCopy
		case command.Suspend != nil:
			if len(completedTasks) != 1 {
				return nil, nil, nil, fmt.Errorf("task %q cannot suspend from a parallel wave", task.TaskID)
			}
			suspend = &taskSuspend{task: task, request: command.Suspend}
			tasks, err := runnable.graph.resolveNextTasksObserved(ctx, task, currentState, func(decision registry.RouteDecision) error {
				return runnable.notifySchedulerEvent(context.WithoutCancel(ctx), config, routeDecisionSchedulerEvent(task.NodeID, decision))
			})
			if err != nil {
				return nil, nil, nil, err
			}
			appendResolvedRouteTasks(tasks, staticTargets, &dynamicTasks)
		case len(command.Goto) > 0:
			for _, target := range command.Goto {
				nodeID, err := runnable.graph.resolveEdgeTarget(string(target))
				if err != nil {
					return nil, nil, nil, fmt.Errorf("task %q goto target %q: %w", task.TaskID, target, err)
				}
				staticTargets[nodeID] = struct{}{}
			}
		case len(command.Send) > 0:
			reducers := runnable.graph.reducers()
			for sendIndex, send := range command.Send {
				nodeID, err := runnable.graph.resolveNodeID(string(send.Target))
				if err != nil {
					return nil, nil, nil, fmt.Errorf("task %q send target %q: %w", task.TaskID, send.Target, err)
				}
				contract, hasContract := runnable.graph.nodeContracts[nodeID]
				issues := state.ValidatePatch(send.Input)
				if hasContract {
					issues = state.ValidateInputPatchByContractWithReducers(currentState, send.Input, contract, reducers)
				}
				if len(issues) > 0 {
					return nil, nil, nil, fmt.Errorf("task %q send %d input: %w", task.TaskID, sendIndex, state.NewValidationError("send input", issues))
				}
				dynamicTasks = append(dynamicTasks, fruntime.GraphTask{
					TaskID:         dynamicTaskID(task, send, sendIndex),
					NodeID:         nodeID,
					Input:          send.Input,
					CorrelationKey: strings.TrimSpace(send.CorrelationKey),
					OrderKey:       strings.TrimSpace(send.OrderKey),
					Order:          sendIndex,
					Dynamic:        true,
				})
			}
		default:
			tasks, err := runnable.graph.resolveNextTasksObserved(ctx, task, currentState, func(decision registry.RouteDecision) error {
				return runnable.notifySchedulerEvent(context.WithoutCancel(ctx), config, routeDecisionSchedulerEvent(task.NodeID, decision))
			})
			if err != nil {
				return nil, nil, nil, err
			}
			appendResolvedRouteTasks(tasks, staticTargets, &dynamicTasks)
		}
	}
	if returnCommand != nil {
		return nil, returnCommand, nil, nil
	}
	staticNodeIDs := sortedNodeIDSet(staticTargets)
	tasks := make([]fruntime.GraphTask, 0, len(staticNodeIDs)+len(dynamicTasks))
	for order, nodeID := range staticNodeIDs {
		tasks = append(tasks, fruntime.NewStaticGraphTask(nodeID, order))
	}
	sort.SliceStable(dynamicTasks, func(leftIndex, rightIndex int) bool {
		left, right := dynamicTasks[leftIndex], dynamicTasks[rightIndex]
		if left.OrderKey != right.OrderKey {
			return left.OrderKey < right.OrderKey
		}
		if left.CorrelationKey != right.CorrelationKey {
			return left.CorrelationKey < right.CorrelationKey
		}
		return left.TaskID < right.TaskID
	})
	for index := range dynamicTasks {
		dynamicTasks[index].Order = len(tasks) + index
	}
	tasks = append(tasks, dynamicTasks...)
	return tasks, nil, suspend, nil
}

func appendResolvedRouteTasks(tasks []fruntime.GraphTask, staticTargets map[string]struct{}, dynamicTasks *[]fruntime.GraphTask) {
	for _, task := range tasks {
		if task.Dynamic {
			*dynamicTasks = append(*dynamicTasks, task)
			continue
		}
		staticTargets[task.NodeID] = struct{}{}
	}
}

func dynamicTaskID(parent fruntime.GraphTask, send core.Send, index int) string {
	identity := fmt.Sprintf("%s\x00%d\x00%s\x00%s\x00%s", parent.TaskID, index, send.Target, strings.TrimSpace(send.CorrelationKey), strings.TrimSpace(send.OrderKey))
	digest := sha256.Sum256([]byte(identity))
	return fmt.Sprintf("send-%x", digest[:12])
}

func (runnable *scheduledRunnable) scheduleNext(nextTasks []fruntime.GraphTask, pendingFanIn map[string]struct{}) ([]fruntime.GraphTask, map[string]struct{}) {
	readyStatic := map[string]fruntime.GraphTask{}
	readyDynamic := make([]fruntime.GraphTask, 0)
	for _, task := range nextTasks {
		if task.Dynamic {
			readyDynamic = append(readyDynamic, task)
			continue
		}
		if task.Failure != nil {
			readyStatic[task.NodeID] = task
			continue
		}
		if runnable.isJoinNode(task.NodeID) {
			pendingFanIn[task.NodeID] = struct{}{}
			continue
		}
		readyStatic[task.NodeID] = task
	}
	runnable.releasePendingFanIn(readyStatic, pendingFanIn)
	ready := make([]fruntime.GraphTask, 0, len(readyStatic)+len(readyDynamic))
	for _, nodeID := range sortedGraphTaskNodeIDs(readyStatic) {
		ready = append(ready, readyStatic[nodeID])
	}
	ready = append(ready, readyDynamic...)
	return ready, pendingFanIn
}

func (runnable *scheduledRunnable) resumeSchedule(startTasks []fruntime.GraphTask, pendingFanIn map[string]struct{}) ([]fruntime.GraphTask, map[string]struct{}) {
	readyStatic := map[string]fruntime.GraphTask{}
	readyDynamic := make([]fruntime.GraphTask, 0)
	for _, task := range startTasks {
		delete(pendingFanIn, task.NodeID)
		if task.Dynamic {
			readyDynamic = append(readyDynamic, task)
		} else {
			readyStatic[task.NodeID] = task
		}
	}
	runnable.releasePendingFanIn(readyStatic, pendingFanIn)
	ready := make([]fruntime.GraphTask, 0, len(readyStatic)+len(readyDynamic))
	for _, nodeID := range sortedGraphTaskNodeIDs(readyStatic) {
		ready = append(ready, readyStatic[nodeID])
	}
	ready = append(ready, readyDynamic...)
	return ready, pendingFanIn
}

func (runnable *scheduledRunnable) releasePendingFanIn(ready map[string]fruntime.GraphTask, pendingFanIn map[string]struct{}) {
	for {
		released := false
		for _, fanInNodeID := range sortedNodeIDSet(pendingFanIn) {
			if runnable.hasUpstreamBlocker(fanInNodeID, ready, pendingFanIn) {
				continue
			}
			delete(pendingFanIn, fanInNodeID)
			ready[fanInNodeID] = fruntime.NewStaticGraphTask(fanInNodeID, len(ready))
			released = true
		}
		if !released {
			return
		}
	}
}

func (runnable *scheduledRunnable) hasUpstreamBlocker(fanInNodeID string, ready map[string]fruntime.GraphTask, pendingFanIn map[string]struct{}) bool {
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

func (runnable *scheduledRunnable) notifyGraphStep(ctx context.Context, config fruntime.SchedulerConfig, completedTasks []fruntime.GraphTask, currentState *state.State) error {
	if config.StepObserver == nil {
		return nil
	}
	if err := config.StepObserver(ctx, fruntime.CloneGraphTasks(completedTasks), currentState); err != nil {
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

func firstNodeInterrupt(tasks []fruntime.GraphTask, taskErrors []error) (*core.NodeInterrupt, fruntime.GraphTask) {
	for index, taskErr := range taskErrors {
		var interrupt *core.NodeInterrupt
		if errors.As(taskErr, &interrupt) {
			if interrupt.NodeID == "" && index < len(tasks) {
				interrupt.NodeID = tasks[index].NodeID
			}
			if index < len(tasks) {
				return interrupt, tasks[index]
			}
			return interrupt, fruntime.GraphTask{NodeID: interrupt.NodeID, TaskID: interrupt.NodeID}
		}
	}
	return nil, fruntime.GraphTask{}
}

func firstError(errorsList []error) error {
	for _, err := range errorsList {
		if err != nil {
			return err
		}
	}
	return nil
}

func hasNonInterruptError(errorsList []error) bool {
	for _, err := range errorsList {
		if err == nil {
			continue
		}
		var interrupt *core.NodeInterrupt
		if !errors.As(err, &interrupt) {
			return true
		}
	}
	return false
}

func errorCount(errorsList []error) int {
	count := 0
	for _, err := range errorsList {
		if err != nil {
			count++
		}
	}
	return count
}

func configuredInterruptTask(tasks []fruntime.GraphTask, config fruntime.SchedulerConfig) (fruntime.GraphTask, bool) {
	for _, task := range tasks {
		for _, configuredNodeID := range config.InterruptAfterNodeIDs {
			if task.NodeID == configuredNodeID {
				return task, true
			}
		}
	}
	return fruntime.GraphTask{}, false
}

func (runnable *scheduledRunnable) activeTasks(tasks []fruntime.GraphTask) ([]fruntime.GraphTask, error) {
	active := make([]fruntime.GraphTask, 0, len(tasks))
	seen := map[string]struct{}{}
	for _, task := range tasks {
		if task.NodeID == "" || task.TaskID == "" {
			return nil, fmt.Errorf("graph task requires task_id and node_id")
		}
		if task.NodeID == endNodeID {
			continue
		}
		if _, err := runnable.graph.resolveNodeID(task.NodeID); err != nil {
			return nil, fmt.Errorf("task %q: %w", task.TaskID, err)
		}
		if _, exists := seen[task.TaskID]; exists {
			return nil, fmt.Errorf("graph task id %q is duplicated in one wave", task.TaskID)
		}
		seen[task.TaskID] = struct{}{}
		active = append(active, task)
	}
	sort.SliceStable(active, func(leftIndex, rightIndex int) bool {
		if active[leftIndex].Order != active[rightIndex].Order {
			return active[leftIndex].Order < active[rightIndex].Order
		}
		return active[leftIndex].TaskID < active[rightIndex].TaskID
	})
	for index := range active {
		active[index].ParallelWaveSize = len(active)
	}
	return active, nil
}

func sortedGraphTaskNodeIDs(tasks map[string]fruntime.GraphTask) []string {
	nodeIDs := make([]string, 0, len(tasks))
	for nodeID := range tasks {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Strings(nodeIDs)
	return nodeIDs
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
