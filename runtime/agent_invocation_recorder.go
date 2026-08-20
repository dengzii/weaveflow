package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/state"
)

type graphAgentInvocationRecorder struct {
	execution    *graphRunnerExecution
	parentTask   GraphTask
	parentStepID string
	baseState    *state.State
}

func (e *graphRunnerExecution) agentInvocationRecorder(task GraphTask, currentState *state.State) AgentInvocationRecorder {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	parentStepID := ""
	if active := e.active[task.TaskID]; active != nil {
		parentStepID = active.step.StepID
	}
	e.mu.Unlock()
	return &graphAgentInvocationRecorder{execution: e, parentTask: task, parentStepID: parentStepID, baseState: currentState}
}

func (recorder *graphAgentInvocationRecorder) Start(ctx context.Context, invocation AgentInvocation) (AgentInvocation, error) {
	if recorder == nil || recorder.execution == nil {
		return invocation, errors.New("agent invocation recorder is unavailable")
	}
	e := recorder.execution
	run := e.currentRun()
	invocation.StepID = stableRuntimeID("agent-step", recorder.parentTask.TaskID, invocation.ID)
	invocation.OperationID = stableRuntimeID("agent-operation", recorder.parentTask.TaskID, invocation.ID)
	now := e.runner.currentTime()
	step := StepRecord{
		StepID: invocation.StepID, RunID: run.RunID, TaskID: recorder.parentTask.TaskID,
		ParentRunID: run.ParentRunID, ParentStepID: run.ParentStepID, ParentTaskID: run.ParentTaskID,
		RootRunID: run.RootRunID, RunPath: append([]string(nil), run.RunPath...), Namespace: run.Namespace,
		NodeID: recorder.parentTask.NodeID, NodeName: invocationNodeName(invocation), OperationKey: invocation.OperationID,
		EffectClass: core.EffectReadOnly, EffectStatus: core.EffectIntent, Status: StepStatusRunning,
		Attempt: 1, StartedAt: now, UpdatedAt: now,
	}
	if err := e.runner.executionStore.AppendStep(ctx, step); err != nil {
		return invocation, fmt.Errorf("append agent invocation step run=%q step=%q: %w", step.RunID, step.StepID, err)
	}
	return invocation, nil
}

func (recorder *graphAgentInvocationRecorder) Checkpoint(ctx context.Context, invocation AgentInvocation, phase string) (AgentInvocation, error) {
	if recorder == nil || recorder.execution == nil {
		return invocation, errors.New("agent invocation recorder is unavailable")
	}
	e := recorder.execution
	currentState, ok := AgentStateFromContext(ctx)
	if !ok || currentState == nil {
		currentState = recorder.baseState
	}
	if currentState == nil {
		return invocation, errors.New("agent invocation state is unavailable")
	}
	checkpointState := currentState.Clone()
	if err := StoreAgentResumeState(checkpointState, invocation.ID, phase); err != nil {
		return invocation, err
	}
	schedule, _, err := LoadGraphSchedule(checkpointState)
	if err != nil {
		return invocation, fmt.Errorf("load agent checkpoint schedule: %w", err)
	}
	if err := StoreGraphSchedule(checkpointState, GraphSchedule{
		CurrentTasks:      CloneGraphTasks([]GraphTask{recorder.parentTask}),
		NextTasks:         schedule.NextTasks,
		PendingFanInTasks: CloneGraphTasks(schedule.PendingFanInTasks),
	}); err != nil {
		return invocation, fmt.Errorf("store agent checkpoint schedule: %w", err)
	}
	run := e.currentRun()
	step, err := e.runner.executionStore.GetStep(ctx, invocation.StepID)
	if err != nil {
		return invocation, fmt.Errorf("load agent invocation step: %w", err)
	}
	checkpointWrite, checkpointEvent, err := e.runner.buildCheckpointWrite(ctx, run, step, recorder.parentTask.NodeID, CheckpointAgent, checkpointState, step.Attempt, nil, nil)
	if err != nil {
		return invocation, fmt.Errorf("build agent invocation checkpoint: %w", err)
	}
	step.CheckpointAfterID = checkpointWrite.Record.CheckpointID
	step.UpdatedAt = e.runner.currentTime()
	run.LastCheckpointID = checkpointWrite.Record.CheckpointID
	run.UpdatedAt = step.UpdatedAt
	commitResult, err := e.runner.commitRuntime(ctx, Commit{
		TransactionID: stableRuntimeID("agent-checkpoint", invocation.ID, phase),
		Run:           &RunWrite{Mode: RunWriteUpdate, Run: run},
		Steps:         []StepWrite{{Mode: StepWriteUpdate, Step: step}},
		Checkpoints:   []CheckpointWrite{checkpointWrite},
		Events:        []Event{checkpointEvent},
	})
	if err != nil {
		return invocation, fmt.Errorf("commit agent invocation checkpoint: %w", err)
	}
	if commitResult.Run != nil {
		e.mu.Lock()
		e.run = *commitResult.Run
		e.mu.Unlock()
	}
	invocation.CheckpointID = checkpointWrite.Record.CheckpointID
	return invocation, nil
}

func (recorder *graphAgentInvocationRecorder) Finish(ctx context.Context, invocation AgentInvocation, invocationErr error) error {
	if recorder == nil || recorder.execution == nil {
		return errors.New("agent invocation recorder is unavailable")
	}
	step, err := recorder.execution.runner.executionStore.GetStep(ctx, invocation.StepID)
	if err != nil {
		return fmt.Errorf("load finished agent invocation step: %w", err)
	}
	now := recorder.execution.runner.currentTime()
	step.UpdatedAt = now
	step.FinishedAt = &now
	if invocationErr == nil {
		step.Status = StepStatusSucceeded
		step.EffectStatus = core.EffectSucceeded
	} else {
		step.Status = StepStatusFailed
		step.EffectStatus = core.EffectFailed
		step.ErrorCode = string(core.ClassifyError(invocationErr))
		step.ErrorMessage = invocationErr.Error()
	}
	if err := recorder.execution.runner.executionStore.UpdateStep(ctx, step); err != nil {
		return fmt.Errorf("update finished agent invocation step: %w", err)
	}
	return nil
}

func invocationNodeName(invocation AgentInvocation) string {
	if invocation.Kind == AgentInvocationTool {
		if name := strings.TrimSpace(invocation.ToolName); name != "" {
			return "Agent Tool: " + name
		}
		return "Agent Tool"
	}
	return "Agent Model"
}

func (recorder *graphAgentInvocationRecorder) String() string {
	if recorder == nil {
		return ""
	}
	return fmt.Sprintf("%s/%s", recorder.parentTask.NodeID, recorder.parentTask.TaskID)
}
