package runtime

import (
	"context"
	"errors"
	"sync"

	langgraph "github.com/smallnest/langgraphgo/graph"
	"go.uber.org/zap"
)

type runnerControlKind string

var logger = zap.NewNop()

func SetLogger(l *zap.Logger) {
	logger = l
}

const (
	runnerControlPause  runnerControlKind = "pause"
	runnerControlCancel runnerControlKind = "cancel"
)

type runnerPendingControl struct {
	kind runnerControlKind
	hit  *BreakpointHit
}

type runnerActiveStep struct {
	step               StepRecord
	attempts           int
	beforeCheckpointID string
	beforeInterrupted  bool
}

type runnerCompletedStep struct {
	step              StepRecord
	afterCheckpointID string
}

type graphRunnerExecution struct {
	runner        *GraphRunner
	run           RunRecord
	skip          *breakpointSkip
	lastState     State
	artifacts     []ArtifactRef
	active        *runnerActiveStep
	lastCompleted *runnerCompletedStep
	pending       *runnerPendingControl
	mu            sync.Mutex
}

func newGraphRunnerExecution(runner *GraphRunner, run RunRecord, initialState State, initialArtifacts []ArtifactRef, skip *breakpointSkip) *graphRunnerExecution {
	state := State{}
	if initialState != nil {
		state = initialState.CloneState()
	}
	return &graphRunnerExecution{
		runner:    runner,
		run:       run,
		skip:      skip,
		lastState: state,
		artifacts: cloneArtifactRefs(initialArtifacts),
	}
}

func (e *graphRunnerExecution) InvokeNode(ctx context.Context, nodeID string, invoke NodeInvoker, state State) (State, error) {
	nodeCtx, err := e.beforeNode(ctx, nodeID, state)
	if err != nil {
		return state, err
	}

	result, invokeErr := invoke(nodeCtx, state.CloneState())
	if invokeErr != nil {
		var interrupt *langgraph.NodeInterrupt
		if errors.As(invokeErr, &interrupt) {
			e.markNodeInterrupt(nodeID)
		}
	}
	return result, invokeErr
}

func (e *graphRunnerExecution) beforeNode(ctx context.Context, nodeID string, state State) (context.Context, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return ctx, err
	}

	if latestRun, err := e.runner.ExecutionStore.GetRun(ctx, e.run.RunID); err == nil {
		e.run = latestRun
	}

	if e.run.CancelRequested {
		logger.Info("cancel interrupt requested",
			zap.String("run_id", e.run.RunID),
			zap.String("node_id", nodeID),
		)
		e.pending = &runnerPendingControl{kind: runnerControlCancel}
		return ctx, &langgraph.NodeInterrupt{Node: nodeID, Value: string(runnerControlCancel)}
	}

	active := e.active
	if active == nil || active.step.NodeID != nodeID {
		step := StepRecord{
			StepID:    newRunnerID(),
			RunID:     e.run.RunID,
			NodeID:    nodeID,
			NodeName:  e.runner.nodeName(nodeID),
			Status:    StepStatusScheduled,
			StartedAt: e.runner.now(),
			UpdatedAt: e.runner.now(),
			Attempt:   1,
		}
		if err := e.runner.ExecutionStore.AppendStep(ctx, step); err != nil {
			return ctx, err
		}

		e.run.CurrentNodeID = step.NodeID
		e.run.LastStepID = step.StepID
		e.run.UpdatedAt = e.runner.now()
		if err := e.runner.ExecutionStore.UpdateRun(ctx, e.run); err != nil {
			return ctx, err
		}

		beforeID, err := e.runner.saveCheckpoint(ctx, e.run, step, nodeID, CheckpointBeforeNode, state, 0, nil, cloneArtifactRefs(e.artifacts))
		if err != nil {
			return ctx, err
		}

		step.CheckpointBeforeID = beforeID
		step.Status = StepStatusRunning
		step.UpdatedAt = e.runner.now()
		if err := e.runner.ExecutionStore.UpdateStep(ctx, step); err != nil {
			return ctx, err
		}

		active = &runnerActiveStep{
			step:               step,
			beforeCheckpointID: beforeID,
		}
		e.active = active
		logger.Debug("nodes scheduled", stepLogFields(step)...)
	}

	active.attempts++
	step := active.step
	logStep := step
	logStep.Attempt = active.attempts

	if active.attempts == 1 {
		if e.run.PauseRequested {
			active.beforeInterrupted = true
			e.pending = &runnerPendingControl{kind: runnerControlPause}
			logger.Info("pause interrupt requested", stepLogFields(logStep)...)
			return ctx, &langgraph.NodeInterrupt{Node: nodeID, Value: string(runnerControlPause)}
		}
		if hit := e.runner.matchBreakpoint(step.NodeID, string(CheckpointBeforeNode), e.skip); hit != nil {
			active.beforeInterrupted = true
			e.pending = &runnerPendingControl{kind: runnerControlPause, hit: hit}
			fields := append(stepLogFields(logStep),
				zap.String("breakpoint_id", hit.BreakpointID),
				zap.String("breakpoint_stage", hit.Stage),
			)
			logger.Info("breakpoint hit before nodes", fields...)
			return ctx, &langgraph.NodeInterrupt{Node: nodeID, Value: hit}
		}

		e.runner.notifyListeners(ctx, langgraph.NodeEventStart, nodeID, state, nil)
		if err := e.runner.publishEvent(ctx, e.run, step.StepID, step.NodeID, EventNodeStarted, map[string]any{
			"node_name": step.NodeName,
		}); err != nil {
			return ctx, err
		}
		logger.Info("nodes started", append(stepLogFields(logStep), stateSummaryFields(state)...)...)
	} else {
		if err := e.runner.publishEvent(ctx, RunRecord{RunID: e.run.RunID}, step.StepID, step.NodeID, EventNodeRetry, map[string]any{
			"attempt": active.attempts - 1,
		}); err != nil {
			return ctx, err
		}
		logger.Warn("nodes retrying", stepLogFields(logStep)...)
	}

	stepID := step.StepID
	nodeID = step.NodeID
	runID := e.run.RunID
	nodeCtx := WithRunnerEventPublisher(ctx, func(eventType EventType, payload any) error {
		return e.runner.publishEvent(ctx, RunRecord{RunID: runID}, stepID, nodeID, eventType, payload)
	})
	nodeCtx = WithRunnerMetadata(nodeCtx, RunnerMetadata{
		RunID:   runID,
		StepID:  stepID,
		NodeID:  nodeID,
		Attempt: active.attempts,
	})
	nodeCtx = WithRunnerArtifactRecorder(nodeCtx, func(ctx context.Context, artifact Artifact) (ArtifactRef, error) {
		ref, err := e.runner.recordArtifact(ctx, artifact)
		if err != nil {
			return ArtifactRef{}, err
		}
		e.appendArtifact(ref)
		return ref, nil
	})
	return nodeCtx, nil
}

func (e *graphRunnerExecution) OnGraphStep(ctx context.Context, nodeID string, state State) error {
	e.mu.Lock()
	active := e.active
	if active == nil {
		e.mu.Unlock()
		return nil
	}
	if active.beforeInterrupted {
		e.mu.Unlock()
		return nil
	}

	step := active.step
	attempts := active.attempts
	run := e.run
	beforeState := e.lastState.CloneState()
	e.mu.Unlock()

	afterID, err := e.runner.saveCheckpoint(ctx, run, step, nodeID, CheckpointAfterNode, state, attempts, nil, e.snapshotArtifacts())
	if err != nil {
		return err
	}
	if err := e.runner.publishStateDiff(ctx, run, step, beforeState, state); err != nil {
		return err
	}

	now := e.runner.now()
	step.Attempt = attempts
	step.Status = StepStatusSucceeded
	step.CheckpointAfterID = afterID
	step.FinishedAt = &now
	step.UpdatedAt = now
	if err := e.runner.ExecutionStore.UpdateStep(ctx, step); err != nil {
		return err
	}

	e.runner.notifyListeners(ctx, langgraph.NodeEventComplete, nodeID, state, nil)
	if err := e.runner.publishEvent(ctx, run, step.StepID, step.NodeID, EventNodeFinished, map[string]any{
		"attempt": attempts,
	}); err != nil {
		return err
	}

	run.LastCheckpointID = afterID
	run.UpdatedAt = e.runner.now()
	if err := e.runner.ExecutionStore.UpdateRun(ctx, run); err != nil {
		return err
	}
	fields := append(stepLogFields(step),
		zap.String("checkpoint_after_id", afterID),
	)
	fields = append(fields, stateSummaryFields(state)...)
	logger.Info("nodes completed", fields...)

	e.mu.Lock()
	e.run = run
	e.lastState = state.CloneState()
	e.lastCompleted = &runnerCompletedStep{
		step:              step,
		afterCheckpointID: afterID,
	}
	e.active = nil
	e.pending = nil
	e.mu.Unlock()
	return nil
}

func (e *graphRunnerExecution) finalizeFailure(ctx context.Context, err error) error {
	e.mu.Lock()
	active := e.active
	if active == nil {
		e.mu.Unlock()
		return nil
	}
	step := active.step
	attempts := active.attempts
	nodeID := step.NodeID
	state := e.lastState.CloneState()
	run := e.run
	e.active = nil
	e.pending = nil
	e.mu.Unlock()

	now := e.runner.now()
	step.Attempt = attempts
	step.Status = StepStatusFailed
	step.ErrorCode = "node_failed"
	step.ErrorMessage = err.Error()
	step.FinishedAt = &now
	step.UpdatedAt = now
	if updateErr := e.runner.ExecutionStore.UpdateStep(ctx, step); updateErr != nil {
		return updateErr
	}
	logger.Error("nodes failed", append(stepLogFields(step), zap.Error(err))...)

	e.runner.notifyListeners(ctx, langgraph.NodeEventError, nodeID, state, err)
	return e.runner.publishEvent(ctx, run, step.StepID, step.NodeID, EventNodeFailed, map[string]any{
		"error":   err.Error(),
		"attempt": attempts,
	})
}

func (e *graphRunnerExecution) currentRun() RunRecord {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.run
}

func (e *graphRunnerExecution) stateOrFallback(state State) State {
	if state != nil {
		return state
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastState.CloneState()
}

func (e *graphRunnerExecution) consumePendingControl() (*runnerPendingControl, *runnerActiveStep) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pending == nil {
		return nil, nil
	}

	control := *e.pending
	e.pending = nil

	var activeCopy *runnerActiveStep
	if e.active != nil {
		copyStep := *e.active
		activeCopy = &copyStep
	}
	return &control, activeCopy
}

func (e *graphRunnerExecution) consumeLastCompleted(nodeID string) *runnerCompletedStep {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.lastCompleted == nil {
		return nil
	}
	if nodeID != "" && e.lastCompleted.step.NodeID != nodeID {
		return nil
	}
	completed := *e.lastCompleted
	e.lastCompleted = nil
	return &completed
}

func (e *graphRunnerExecution) appendArtifact(ref ArtifactRef) {
	if ref.ID == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.artifacts = append(e.artifacts, ref)
}

func (e *graphRunnerExecution) snapshotArtifacts() []ArtifactRef {
	e.mu.Lock()
	defer e.mu.Unlock()
	return cloneArtifactRefs(e.artifacts)
}

func (e *graphRunnerExecution) afterInterruptNodes() ([]string, error) {
	graph := e.runner.runnerGraph()
	if graph == nil {
		return nil, errors.New("graph runner graph is nil")
	}
	return graph.AfterInterruptNodes(e.runner.Breakpoints)
}

func (e *graphRunnerExecution) markNodeInterrupt(nodeID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.active == nil || e.active.step.NodeID != nodeID {
		return
	}
	/// make sure the nodes resume at the same nodes after restart
	e.active.beforeInterrupted = true
	e.pending = &runnerPendingControl{kind: runnerControlPause}
	logStep := e.active.step
	logStep.Attempt = e.active.attempts
	logger.Info("nodes interrupt captured", stepLogFields(logStep)...)
}

type runnerGraphCallbacks struct {
	langgraph.NoOpCallbackHandler
	execution *graphRunnerExecution
}

func (c *runnerGraphCallbacks) OnGraphStep(ctx context.Context, stepNodeID string, state any) {
	if c == nil || c.execution == nil {
		return
	}
	typed, ok := state.(State)
	if !ok {
		return
	}
	_ = c.execution.OnGraphStep(ctx, stepNodeID, typed)
}
