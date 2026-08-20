package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/state"
)

type preparedFork struct {
	result  ForkResult
	state   *state.State
	guard   ExecutionLeaseGuard
	created bool
}

func (r *GraphRunner) ForkFromCheckpoint(ctx context.Context, request ForkRequest) (ForkResult, *state.State, error) {
	prepared, err := r.prepareFork(ctx, request)
	if err != nil {
		return ForkResult{}, nil, err
	}
	if !prepared.created {
		return prepared.result, prepared.state, nil
	}
	executionCtx, heartbeat := r.startLeaseHeartbeat(ctx, prepared.guard)
	finished, finalState, runErr := r.Resume(executionCtx, prepared.result.Run.RunID, nil)
	leaseErr := r.finishExecutionLease(executionCtx, prepared.guard, heartbeat)
	if finished.RunID != "" {
		prepared.result.Run = finished
	}
	return prepared.result, finalState, errors.Join(runErr, leaseErr)
}

func (r *GraphRunner) ForkFromCheckpointAsync(ctx context.Context, request ForkRequest) (ForkResult, <-chan struct{}, error) {
	prepared, err := r.prepareFork(ctx, request)
	if err != nil {
		return ForkResult{}, nil, err
	}
	done := make(chan struct{})
	if !prepared.created {
		close(done)
		return prepared.result, done, nil
	}
	go func() {
		defer close(done)
		executionCtx, heartbeat := r.startLeaseHeartbeat(ctx, prepared.guard)
		finished, _, runErr := r.Resume(executionCtx, prepared.result.Run.RunID, nil)
		if runErr != nil {
			r.failAsyncExecution(context.WithoutCancel(executionCtx), prepared.result.Run, prepared.state, "fork_execution_failed", runErr.Error())
		}
		if finishErr := r.finishExecutionLease(executionCtx, prepared.guard, heartbeat); finishErr != nil && finished.RunID == "" {
			r.failAsyncExecution(context.WithoutCancel(executionCtx), prepared.result.Run, prepared.state, "fork_lease_failed", finishErr.Error())
		}
	}()
	return prepared.result, done, nil
}

func (r *GraphRunner) prepareFork(ctx context.Context, request ForkRequest) (preparedFork, error) {
	ctx = normalizeRunnerContext(ctx)
	if r == nil {
		return preparedFork{}, errors.New("graph runner is nil")
	}
	if err := r.validate(); err != nil {
		return preparedFork{}, err
	}
	request.SourceRunID = strings.TrimSpace(request.SourceRunID)
	request.SourceCheckpointID = strings.TrimSpace(request.SourceCheckpointID)
	request.RequestKey = strings.TrimSpace(request.RequestKey)
	if request.SourceRunID == "" || request.SourceCheckpointID == "" || request.RequestKey == "" {
		return preparedFork{}, errors.New("fork requires source run ID, source checkpoint ID, and request key")
	}
	r.childRunMu.Lock()
	defer r.childRunMu.Unlock()
	sourceRun, err := r.executionStore.GetRun(ctx, request.SourceRunID)
	if err != nil {
		return preparedFork{}, err
	}
	if sourceRun.Deletion != nil {
		return preparedFork{}, fmt.Errorf("source run %q is reserved for deletion", sourceRun.RunID)
	}
	if err := r.validateRunGraphHash(sourceRun); err != nil {
		return preparedFork{}, err
	}
	if err := r.ensureRunEffectsResolved(ctx, sourceRun); err != nil {
		return preparedFork{}, err
	}
	checkpoint, err := r.LoadCheckpointState(ctx, request.SourceCheckpointID)
	if err != nil {
		return preparedFork{}, err
	}
	if err := validateCheckpointRun(sourceRun, checkpoint); err != nil {
		return preparedFork{}, err
	}
	if checkpoint.Record.Stage == CheckpointFinal {
		return preparedFork{}, fmt.Errorf("checkpoint %q is final and cannot be forked", checkpoint.Record.CheckpointID)
	}
	if err := r.validateIndependentCheckpoint(checkpoint); err != nil {
		return preparedFork{}, err
	}
	forkID := stableRuntimeID("fork-run", request.SourceRunID, request.SourceCheckpointID, request.RequestKey)
	if existing, getErr := r.executionStore.GetRun(ctx, forkID); getErr == nil {
		if existing.SourceRunID != request.SourceRunID || existing.SourceCheckpointID != request.SourceCheckpointID || existing.ForkRequestKey != request.RequestKey {
			return preparedFork{}, fmt.Errorf("fork request key %q resolves to a different run", request.RequestKey)
		}
		stateValue, stateErr := r.loadRunState(ctx, existing)
		return preparedFork{result: ForkResult{Run: existing, SourceRunID: request.SourceRunID, SourceCheckpointID: request.SourceCheckpointID, RequestKey: request.RequestKey}, state: stateValue}, stateErr
	} else if !errors.Is(getErr, ErrRunnerRecordNotFound) {
		return preparedFork{}, getErr
	}
	forkState, err := state.MergeResumeInput(checkpoint.Business, request.Input)
	if err != nil {
		return preparedFork{}, err
	}
	if issues := state.ValidateStateBySchemas(forkState, r.stateSchemas); len(issues) > 0 {
		return preparedFork{}, state.NewValidationError("fork input", issues)
	}
	schedule, _, err := LoadGraphSchedule(forkState)
	if err != nil {
		return preparedFork{}, fmt.Errorf("load fork graph schedule: %w", err)
	}
	taskIDs := make(map[string]string)
	schedule = remapForkSchedule(forkID, schedule, taskIDs)
	if err := StoreGraphSchedule(forkState, schedule); err != nil {
		return preparedFork{}, err
	}
	if err := remapForkAfterNodeCommand(forkState, taskIDs); err != nil {
		return preparedFork{}, err
	}
	now := r.currentTime()
	stepID := newRunnerID()
	taskID := checkpoint.Record.TaskID
	if mapped, ok := taskIDs[taskID]; ok {
		taskID = mapped
	}
	if taskID == "" && len(schedule.CurrentTasks) > 0 {
		taskID = schedule.CurrentTasks[0].TaskID
	}
	nodeID := checkpoint.Record.NodeID
	if nodeID == "" && len(schedule.CurrentTasks) > 0 {
		nodeID = schedule.CurrentTasks[0].NodeID
	}
	run := RunRecord{
		RunID: forkID, GraphID: sourceRun.GraphID, GraphVersion: sourceRun.GraphVersion,
		GraphHash: sourceRun.GraphHash, GraphSnapshotHash: sourceRun.GraphSnapshotHash, GraphSessionID: sourceRun.GraphSessionID,
		SourceRunID: request.SourceRunID, SourceCheckpointID: request.SourceCheckpointID, ForkRequestKey: request.RequestKey,
		Status: RunStatusRunning, EntryNodeID: sourceRun.EntryNodeID, CurrentNodeID: nodeID,
		CurrentNodeIDs: append([]string(nil), checkpoint.Runtime.CurrentNodeIDs...),
		NextNodeIDs:    append([]string(nil), checkpoint.Runtime.NextNodeIDs...),
		ParallelWaveID: checkpoint.Runtime.ParallelWaveID, LastStepID: stepID,
		RootRunID: forkID, RunPath: []string{forkID}, Namespace: forkID,
		StartedAt: now, UpdatedAt: now, ExecutionLease: r.newExecutionLease(nil, now),
	}
	if len(run.CurrentNodeIDs) == 0 && nodeID != "" {
		run.CurrentNodeIDs = []string{nodeID}
	}
	step := StepRecord{StepID: stepID, RunID: forkID, TaskID: taskID, NodeID: nodeID, NodeName: r.runnerGraph().NodeName(nodeID), Status: StepStatusScheduled, Attempt: 0, StartedAt: now, UpdatedAt: now}
	checkpointWrite, checkpointEvent, err := r.buildCheckpointWrite(ctx, run, step, nodeID, checkpoint.Record.Stage, forkState, checkpoint.Runtime.RetryCount, nil, nil)
	if err != nil {
		return preparedFork{}, err
	}
	run.LastCheckpointID = checkpointWrite.Record.CheckpointID
	createdEvent, err := r.buildEvent(run, "", "", "", EventRunCreated, map[string]any{"entry_node_id": nodeID, "source_run_id": request.SourceRunID, "source_checkpoint_id": request.SourceCheckpointID})
	if err != nil {
		return preparedFork{}, err
	}
	startedEvent, err := r.buildEvent(run, "", "", "", EventRunStarted, map[string]any{"fork_request_key": request.RequestKey})
	if err != nil {
		return preparedFork{}, err
	}
	forkedEvent, err := r.buildEvent(run, step.StepID, taskID, nodeID, EventRunForked, map[string]any{"source_run_id": request.SourceRunID, "source_checkpoint_id": request.SourceCheckpointID, "request_key": request.RequestKey})
	if err != nil {
		return preparedFork{}, err
	}
	commit := Commit{TransactionID: stableRuntimeID("fork-commit", forkID), Run: &RunWrite{Mode: RunWriteCreate, Run: run}, Checkpoints: []CheckpointWrite{checkpointWrite}, Events: []Event{createdEvent, startedEvent, checkpointEvent, forkedEvent}}
	commitResult, err := r.commitRuntime(ctx, commit)
	if err != nil {
		return preparedFork{}, err
	}
	if commitResult.Run != nil {
		run = *commitResult.Run
	}
	guard, ok := executionLeaseGuard(run)
	if !ok {
		return preparedFork{}, errors.New("fork run did not receive an execution lease")
	}
	return preparedFork{result: ForkResult{Run: run, SourceRunID: request.SourceRunID, SourceCheckpointID: request.SourceCheckpointID, RequestKey: request.RequestKey}, state: forkState, guard: guard, created: true}, nil
}

func (r *GraphRunner) loadRunState(ctx context.Context, run RunRecord) (*state.State, error) {
	if run.LastCheckpointID == "" {
		return state.NewState(), nil
	}
	checkpoint, err := r.LoadCheckpointState(ctx, run.LastCheckpointID)
	if err != nil {
		return nil, err
	}
	return checkpoint.Business, nil
}

func (r *GraphRunner) CompareRuns(ctx context.Context, leftRunID, rightRunID string) (RunComparison, error) {
	ctx = normalizeRunnerContext(ctx)
	leftRunID = strings.TrimSpace(leftRunID)
	rightRunID = strings.TrimSpace(rightRunID)
	if leftRunID == "" || rightRunID == "" {
		return RunComparison{}, errors.New("two run IDs are required for comparison")
	}
	left, err := r.GetRun(ctx, leftRunID)
	if err != nil {
		return RunComparison{}, err
	}
	right, err := r.GetRun(ctx, rightRunID)
	if err != nil {
		return RunComparison{}, err
	}
	if left.GraphID != right.GraphID || left.GraphHash != right.GraphHash || left.GraphSnapshotHash != right.GraphSnapshotHash {
		return RunComparison{}, errors.New("runs must use the same graph snapshot")
	}
	leftSteps, err := r.ListSteps(ctx, leftRunID)
	if err != nil {
		return RunComparison{}, err
	}
	rightSteps, err := r.ListSteps(ctx, rightRunID)
	if err != nil {
		return RunComparison{}, err
	}
	leftEvents, err := r.ListEvents(leftRunID)
	if err != nil {
		return RunComparison{}, err
	}
	rightEvents, err := r.ListEvents(rightRunID)
	if err != nil {
		return RunComparison{}, err
	}
	leftArtifacts, err := r.ListArtifacts(ctx, leftRunID)
	if err != nil {
		return RunComparison{}, err
	}
	rightArtifacts, err := r.ListArtifacts(ctx, rightRunID)
	if err != nil {
		return RunComparison{}, err
	}
	comparison := RunComparison{Left: left, Right: right, LeftSteps: leftSteps, RightSteps: rightSteps, LeftEvents: leftEvents, RightEvents: rightEvents, LeftArtifacts: leftArtifacts, RightArtifacts: rightArtifacts}
	if left.LastCheckpointID != "" && right.LastCheckpointID != "" {
		leftCheckpoint, leftErr := r.LoadCheckpointState(ctx, left.LastCheckpointID)
		rightCheckpoint, rightErr := r.LoadCheckpointState(ctx, right.LastCheckpointID)
		if leftErr != nil || rightErr != nil {
			return RunComparison{}, errors.Join(leftErr, rightErr)
		}
		changes, err := r.codec.Diff(leftCheckpoint.Snapshot, rightCheckpoint.Snapshot)
		if err != nil {
			return RunComparison{}, err
		}
		comparison.StateChanges = redactStateChanges(ctx, changes)
		comparison.CheckpointID = leftCheckpoint.Record.CheckpointID
		comparison.OtherCheckpointID = rightCheckpoint.Record.CheckpointID
	}
	return comparison, nil
}

func remapForkSchedule(forkID string, schedule GraphSchedule, taskIDs map[string]string) GraphSchedule {
	remap := func(tasks []GraphTask, bucket string) []GraphTask {
		cloned := CloneGraphTasks(tasks)
		for index := range cloned {
			oldID := cloned[index].TaskID
			if oldID == "" {
				oldID = fmt.Sprintf("%s-%d", bucket, index)
			}
			newID, ok := taskIDs[oldID]
			if !ok {
				newID = stableRuntimeID("fork-task", forkID, bucket, oldID, fmt.Sprintf("%d", index))
				taskIDs[oldID] = newID
			}
			cloned[index].TaskID = newID
			cloned[index].OperationID = stableRuntimeID("fork-operation", forkID, bucket, oldID, fmt.Sprintf("%d", index))
		}
		return cloned
	}
	return GraphSchedule{CurrentTasks: remap(schedule.CurrentTasks, "current"), NextTasks: remap(schedule.NextTasks, "next"), PendingFanInTasks: remap(schedule.PendingFanInTasks, "pending")}
}

func remapForkAfterNodeCommand(currentState *state.State, taskIDs map[string]string) error {
	command, found, err := loadAfterNodeCommand(currentState)
	if err != nil || !found {
		return err
	}
	if mapped, ok := taskIDs[command.TaskID]; ok {
		command.TaskID = mapped
		return state.SetPath(currentState, afterNodeCommandPath.String(), command)
	}
	return nil
}

func redactStateChanges(ctx context.Context, changes []state.Change) []state.Change {
	redacted := make([]state.Change, len(changes))
	for index, change := range changes {
		redacted[index] = change
		redacted[index].Before = redactPersistedValue(ctx, change.Before)
		redacted[index].After = redactPersistedValue(ctx, change.After)
	}
	return redacted
}
