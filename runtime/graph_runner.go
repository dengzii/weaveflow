package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	langgraph "github.com/smallnest/langgraphgo/graph"
	"go.uber.org/zap"
)

type GraphRunner struct {
	graph           RunnerGraph
	ExecutionStore  ExecutionStore
	CheckpointStore CheckpointStore
	ArtifactStore   ArtifactStore
	StateCodec      StateCodec
	EventSink       EventSink
	GraphID         string
	GraphVersion    string
	Breakpoints     []Breakpoint
	Now             func() time.Time
}

func NewGraphRunner(graph RunnerGraph, executionStore ExecutionStore, checkpointStore CheckpointStore, codec StateCodec, eventSink EventSink) *GraphRunner {
	return &GraphRunner{
		graph:           graph,
		ExecutionStore:  executionStore,
		CheckpointStore: checkpointStore,
		StateCodec:      codec,
		EventSink:       eventSink,
		Now:             time.Now,
	}
}

func (r *GraphRunner) Start(ctx context.Context, initialState State) (RunRecord, State, error) {
	if err := r.validate(); err != nil {
		return RunRecord{}, initialState, err
	}

	if initialState == nil {
		initialState = State{}
	}

	now := r.now()
	entryPoint := r.entryPointID()
	run := RunRecord{
		RunID:        newRunnerID(),
		GraphID:      r.graphID(),
		GraphVersion: r.graphVersion(),
		Status:       RunStatusPending,
		EntryNodeID:  entryPoint,
		StartedAt:    now,
		UpdatedAt:    now,
	}
	if err := r.ExecutionStore.CreateRun(ctx, run); err != nil {
		return RunRecord{}, initialState, err
	}
	if err := r.publishEvent(ctx, run, "", "", EventRunCreated, map[string]any{
		"entry_node_id": run.EntryNodeID,
	}); err != nil {
		return RunRecord{}, initialState, err
	}

	run.Status = RunStatusRunning
	run.CurrentNodeID = run.EntryNodeID
	run.UpdatedAt = r.now()
	if err := r.ExecutionStore.UpdateRun(ctx, run); err != nil {
		return RunRecord{}, initialState, err
	}
	if err := r.publishEvent(ctx, run, "", "", EventRunStarted, nil); err != nil {
		return RunRecord{}, initialState, err
	}
	logger.Info("run started", append(runLogFields(run), stateSummaryFields(initialState)...)...)

	return r.execute(ctx, run, initialState.CloneState(), entryPoint, nil, nil)
}

func (r *GraphRunner) Resume(ctx context.Context, runID string, input State) (RunRecord, State, error) {
	if err := r.validate(); err != nil {
		return RunRecord{}, nil, err
	}

	run, err := r.ExecutionStore.GetRun(ctx, runID)
	if err != nil {
		return RunRecord{}, nil, err
	}
	if strings.TrimSpace(run.LastCheckpointID) == "" {
		return RunRecord{}, nil, fmt.Errorf("resume run %q: no checkpoint available", runID)
	}

	checkpoint, err := r.LoadCheckpointState(ctx, run.LastCheckpointID)
	if err != nil {
		return RunRecord{}, nil, err
	}
	logger.Info("checkpoint loaded",
		zap.String("run_id", run.RunID),
		zap.String("status", string(run.Status)),
		zap.String("checkpoint_id", run.LastCheckpointID),
		zap.String("current_node_id", checkpoint.Runtime.CurrentNodeID),
		zap.String("current_step_id", checkpoint.Runtime.CurrentStepID),
		zap.Int("artifact_count", len(checkpoint.Artifacts)),
	)

	switch {
	case isResumableRunStatus(run.Status):
		return r.resumeExistingRun(ctx, run, checkpoint, input)
	case isContinuableRunStatus(run.Status):
		return r.continueRun(ctx, run, checkpoint, input)
	default:
		return RunRecord{}, nil, fmt.Errorf("run %q status %q is not resumable", runID, run.Status)
	}
}

func (r *GraphRunner) GetResumableRun(ctx context.Context) (*RunRecord, error) {
	return r.latestCheckpointedRun(ctx, isResumableRunStatus)
}

func (r *GraphRunner) GetContinuableRun(ctx context.Context) (*RunRecord, error) {
	return r.latestCheckpointedRun(ctx, isContinuableRunStatus)
}

func (r *GraphRunner) latestCheckpointedRun(ctx context.Context, predicate func(RunStatus) bool) (*RunRecord, error) {
	runs, err := r.ListRuns(ctx, RunFilter{})
	if err != nil {
		return nil, err
	}
	var candidate *RunRecord
	for i := range runs {
		run := runs[i]
		if run.LastCheckpointID == "" {
			continue
		}
		if predicate != nil && !predicate(run.Status) {
			continue
		}
		if candidate == nil || candidate.UpdatedAt.Before(run.UpdatedAt) {
			candidate = &run
		}
	}
	return candidate, nil
}

func isResumableRunStatus(status RunStatus) bool {
	switch status {
	case RunStatusPaused, RunStatusRunning, RunStatusPending:
		return true
	default:
		return false
	}
}

func isContinuableRunStatus(status RunStatus) bool {
	if isResumableRunStatus(status) {
		return true
	}
	switch status {
	case RunStatusCompleted, RunStatusFailed, RunStatusCanceled:
		return true
	default:
		return false
	}
}

func (r *GraphRunner) execute(ctx context.Context, run RunRecord, state State, startNode string, skip *breakpointSkip, artifacts []ArtifactRef) (RunRecord, State, error) {
	execution := newGraphRunnerExecution(r, run, state, artifacts, skip)
	runnable, err := r.runnerGraph().CompileForRunner(execution)
	if err != nil {
		return r.failRun(ctx, run, state, "compile_failed", err.Error())
	}

	afterNodes, err := execution.afterInterruptNodes()
	if err != nil {
		return r.failRun(ctx, run, state, "config_failed", err.Error())
	}

	config := &langgraph.Config{
		Callbacks: []langgraph.CallbackHandler{
			&runnerGraphCallbacks{execution: execution},
		},
	}
	if startNode != "" {
		config.ResumeFrom = []string{startNode}
	}
	if len(afterNodes) > 0 {
		config.InterruptAfter = afterNodes
	}
	fields := append(runLogFields(run),
		zap.String("start_node", startNode),
		zap.Int("breakpoint_count", len(r.Breakpoints)),
		zap.Int("interrupt_after_count", len(afterNodes)),
		zap.Int("artifact_count", len(artifacts)),
	)
	fields = append(fields, stateSummaryFields(state)...)
	logger.Info("run executing", fields...)

	finalState, invokeErr := runnable.InvokeWithConfig(ctx, state.CloneState(), config)
	finalState = execution.stateOrFallback(finalState)
	if invokeErr == nil {
		return r.completeRun(ctx, execution.currentRun(), finalState)
	}

	var interrupt *langgraph.GraphInterrupt
	if errors.As(invokeErr, &interrupt) {
		return r.handleInterrupt(ctx, execution, finalState, interrupt)
	}

	if err := execution.finalizeFailure(ctx, invokeErr); err != nil {
		return RunRecord{}, finalState, err
	}
	return r.failRun(ctx, execution.currentRun(), finalState, "node_failed", invokeErr.Error())
}

func (r *GraphRunner) resumeExistingRun(ctx context.Context, run RunRecord, checkpoint RestoredCheckpoint, input State) (RunRecord, State, error) {
	var err error
	if checkpoint.Business, err = mergeResumeInput(checkpoint.Business, input); err != nil {
		return RunRecord{}, nil, err
	}

	startNode, skip, err := r.resumeTarget(checkpoint.Record, checkpoint.Business)
	if err != nil {
		return RunRecord{}, nil, err
	}

	run.Status = RunStatusRunning
	run.PauseRequested = false
	run.CancelRequested = false
	run.ErrorCode = ""
	run.ErrorMessage = ""
	run.FinishedAt = nil
	if checkpoint.Runtime.CurrentStepID != "" {
		run.LastStepID = checkpoint.Runtime.CurrentStepID
	}
	if startNode == langgraph.END {
		now := r.now()
		run.Status = RunStatusCompleted
		run.UpdatedAt = now
		run.FinishedAt = &now
		if err := r.ExecutionStore.UpdateRun(ctx, run); err != nil {
			return RunRecord{}, nil, err
		}
		logger.Info("resume resolved to completed run", append(runLogFields(run), stateSummaryFields(checkpoint.Business)...)...)
		return run, checkpoint.Business, nil
	}
	run.CurrentNodeID = checkpoint.Runtime.CurrentNodeID
	if checkpoint.Record.Stage != CheckpointBeforeNode || run.CurrentNodeID == "" {
		run.CurrentNodeID = startNode
	}
	run.UpdatedAt = r.now()
	if err := r.ExecutionStore.UpdateRun(ctx, run); err != nil {
		return RunRecord{}, nil, err
	}
	if err := r.publishEvent(ctx, run, "", "", EventRunResumed, map[string]any{
		"checkpoint_id": checkpoint.Record.CheckpointID,
		"node_id":       run.CurrentNodeID,
	}); err != nil {
		return RunRecord{}, nil, err
	}

	fields := append(runLogFields(run),
		zap.String("start_node", startNode),
		zap.String("resume_checkpoint_id", checkpoint.Record.CheckpointID),
		zap.Int("artifact_count", len(checkpoint.Artifacts)),
	)
	fields = append(fields, stateSummaryFields(checkpoint.Business)...)
	logger.Info("resuming run", fields...)
	return r.execute(ctx, run, checkpoint.Business, startNode, skip, checkpoint.Artifacts)
}

func (r *GraphRunner) continueRun(ctx context.Context, run RunRecord, checkpoint RestoredCheckpoint, input State) (RunRecord, State, error) {
	state, err := prepareContinuationState(checkpoint.Business, input)
	if err != nil {
		return RunRecord{}, nil, err
	}

	fields := []zap.Field{
		zap.String("run_id", run.RunID),
		zap.String("status", string(run.Status)),
		zap.String("checkpoint_id", checkpoint.Record.CheckpointID),
		zap.Int("artifact_count", len(checkpoint.Artifacts)),
	}
	fields = append(fields, stateSummaryFields(state)...)
	logger.Info("continuing run as new execution", fields...)
	return r.Start(ctx, state)
}

func (r *GraphRunner) GetRun(ctx context.Context, runID string) (RunRecord, error) {
	if r == nil || r.ExecutionStore == nil {
		return RunRecord{}, errors.New("graph runner execution store is nil")
	}
	return r.ExecutionStore.GetRun(ctx, runID)
}

func (r *GraphRunner) ListRuns(ctx context.Context, filter RunFilter) ([]RunRecord, error) {
	if r == nil || r.ExecutionStore == nil {
		return nil, errors.New("graph runner execution store is nil")
	}
	return r.ExecutionStore.ListRuns(ctx, filter)
}

func (r *GraphRunner) ListSteps(ctx context.Context, runID string) ([]StepRecord, error) {
	if r == nil || r.ExecutionStore == nil {
		return nil, errors.New("graph runner execution store is nil")
	}
	return r.ExecutionStore.ListSteps(ctx, runID)
}

func (r *GraphRunner) ListCheckpoints(ctx context.Context, runID string) ([]CheckpointRecord, error) {
	if r == nil || r.CheckpointStore == nil {
		return nil, errors.New("graph runner checkpoint store is nil")
	}
	return r.CheckpointStore.List(ctx, runID)
}

func (r *GraphRunner) ListArtifacts(ctx context.Context, runID string) ([]ArtifactRef, error) {
	if r == nil || r.ArtifactStore == nil {
		return nil, errors.New("graph runner artifact store is nil")
	}
	return r.ArtifactStore.List(ctx, runID)
}

func (r *GraphRunner) LoadArtifact(ctx context.Context, ref ArtifactRef) (Artifact, error) {
	if r == nil || r.ArtifactStore == nil {
		return Artifact{}, errors.New("graph runner artifact store is nil")
	}
	return r.ArtifactStore.Load(ctx, ref)
}

func (r *GraphRunner) LoadCheckpointState(ctx context.Context, checkpointID string) (RestoredCheckpoint, error) {
	if r == nil {
		return RestoredCheckpoint{}, errors.New("graph runner is nil")
	}
	if r.CheckpointStore == nil {
		return RestoredCheckpoint{}, errors.New("graph runner checkpoint store is nil")
	}
	if r.StateCodec == nil {
		return RestoredCheckpoint{}, errors.New("graph runner state codec is nil")
	}

	record, payload, err := r.CheckpointStore.Load(ctx, checkpointID)
	if err != nil {
		return RestoredCheckpoint{}, err
	}

	snapshot, err := r.StateCodec.Decode(payload)
	if err != nil {
		return RestoredCheckpoint{}, err
	}
	restored, err := RestoreStateSnapshot(snapshot)
	if err != nil {
		return RestoredCheckpoint{}, err
	}

	result := RestoredCheckpoint{
		Record:    record,
		Snapshot:  restored.Snapshot,
		Business:  restored.Business,
		Runtime:   restored.Runtime,
		Artifacts: restored.Artifacts,
	}
	if err := r.validateRestoredCheckpoint(result); err != nil {
		return RestoredCheckpoint{}, err
	}
	return result, nil
}

func (r *GraphRunner) ListEvents(runID string) ([]Event, error) {
	if r == nil || r.EventSink == nil {
		return nil, errors.New("graph runner event sink is nil")
	}
	reader, ok := r.EventSink.(EventReader)
	if !ok {
		return nil, errors.New("graph runner event sink does not support listing events")
	}
	return reader.ListEvents(runID)
}

func (r *GraphRunner) Pause(ctx context.Context, runID string) error {
	run, err := r.ExecutionStore.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	run.PauseRequested = true
	run.UpdatedAt = r.now()
	if err := r.ExecutionStore.UpdateRun(ctx, run); err != nil {
		return err
	}
	logger.Info("pause requested", runLogFields(run)...)
	return r.publishEvent(ctx, run, "", "", EventRunPauseRequested, nil)
}

func (r *GraphRunner) Cancel(ctx context.Context, runID string) error {
	run, err := r.ExecutionStore.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	run.CancelRequested = true
	run.UpdatedAt = r.now()
	if err := r.ExecutionStore.UpdateRun(ctx, run); err != nil {
		return err
	}
	logger.Info("cancel requested", runLogFields(run)...)
	return r.publishEvent(ctx, run, "", "", EventRunCancelRequested, nil)
}

func (r *GraphRunner) handleInterrupt(ctx context.Context, execution *graphRunnerExecution, state State, interrupt *langgraph.GraphInterrupt) (RunRecord, State, error) {
	run := execution.currentRun()
	fields := append(runLogFields(run),
		zap.String("interrupt_node_id", interrupt.Node),
		zap.String("interrupt_reason", interrupt.Error()),
	)
	fields = append(fields, stateSummaryFields(state)...)
	logger.Info("run interrupt", fields...)

	if control, active := execution.consumePendingControl(); control != nil {
		switch control.kind {
		case runnerControlCancel:
			return r.cancelRun(ctx, run, state)
		case runnerControlPause:
			if active == nil {
				return r.failRun(ctx, run, state, "interrupt_failed", "pause interrupt missing active step")
			}
			return r.pauseRun(ctx, run, state, active.step, active.beforeCheckpointID, control.hit)
		}
	}

	if hit := r.matchBreakpoint(interrupt.Node, string(CheckpointAfterNode), nil); hit != nil {
		completed := execution.consumeLastCompleted(interrupt.Node)
		if completed == nil {
			return r.failRun(ctx, run, state, "interrupt_failed", fmt.Sprintf("after-node interrupt missing completed step for %q", interrupt.Node))
		}
		return r.pauseRun(ctx, run, state, completed.step, completed.afterCheckpointID, hit)
	}

	if completed := execution.consumeLastCompleted(interrupt.Node); completed != nil {
		return r.pauseRun(ctx, run, state, completed.step, completed.afterCheckpointID, nil)
	}

	return r.failRun(ctx, run, state, "interrupt_failed", interrupt.Error())
}

func (r *GraphRunner) completeRun(ctx context.Context, run RunRecord, state State) (RunRecord, State, error) {
	now := r.now()
	run.Status = RunStatusCompleted
	run.CurrentNodeID = ""
	run.UpdatedAt = now
	run.FinishedAt = &now
	if err := r.ExecutionStore.UpdateRun(ctx, run); err != nil {
		return RunRecord{}, state, err
	}
	logger.Info("run completed", append(runLogFields(run), stateSummaryFields(state)...)...)
	if err := r.publishEvent(ctx, run, run.LastStepID, "", EventRunFinished, nil); err != nil {
		return RunRecord{}, state, err
	}
	return run, state, nil
}

func (r *GraphRunner) cancelRun(ctx context.Context, run RunRecord, state State) (RunRecord, State, error) {
	now := r.now()
	run.Status = RunStatusCanceled
	run.UpdatedAt = now
	run.FinishedAt = &now
	if err := r.ExecutionStore.UpdateRun(ctx, run); err != nil {
		return RunRecord{}, state, err
	}
	logger.Info("run canceled", append(runLogFields(run), stateSummaryFields(state)...)...)
	if err := r.publishEvent(ctx, run, "", run.CurrentNodeID, EventRunCanceled, nil); err != nil {
		return RunRecord{}, state, err
	}
	return run, state, nil
}

func (r *GraphRunner) saveCheckpoint(ctx context.Context, run RunRecord, step StepRecord, nodeID string, stage CheckpointStage, state State, attempts int, hit *BreakpointHit, artifacts []ArtifactRef) (string, error) {
	snapshot, err := SnapshotFromStateWithRuntime(state, RuntimeState{
		RunID:           run.RunID,
		CurrentStepID:   step.StepID,
		CurrentNodeID:   nodeID,
		Status:          string(run.Status),
		RetryCount:      attempts,
		PauseRequested:  run.PauseRequested,
		CancelRequested: run.CancelRequested,
		BreakpointHit:   hit,
	}, artifacts)
	if err != nil {
		return "", err
	}
	snapshot.Version = r.StateCodec.Version()

	payload, err := r.StateCodec.Encode(snapshot)
	if err != nil {
		return "", err
	}

	record := CheckpointRecord{
		CheckpointID: newRunnerID(),
		RunID:        run.RunID,
		StepID:       step.StepID,
		NodeID:       nodeID,
		Stage:        stage,
		StateCodec:   r.StateCodec.Name(),
		StateVersion: r.StateCodec.Version(),
		CreatedAt:    r.now(),
	}
	if err := r.CheckpointStore.Save(ctx, record, payload); err != nil {
		return "", err
	}
	fields := append(checkpointLogFields(record),
		zap.Int("payload_bytes", len(payload)),
		zap.Int("attempt", attempts),
		zap.Int("artifact_count", len(artifacts)),
	)
	if hit != nil {
		fields = append(fields,
			zap.String("breakpoint_id", hit.BreakpointID),
			zap.String("breakpoint_stage", hit.Stage),
		)
	}
	logger.Debug("checkpoint saved", fields...)
	if err := r.publishEvent(ctx, run, step.StepID, record.NodeID, EventCheckpointCreated, map[string]any{
		"checkpoint_id": record.CheckpointID,
		"stage":         stage,
	}); err != nil {
		return "", err
	}
	return record.CheckpointID, nil
}

func (r *GraphRunner) publishStateDiff(ctx context.Context, run RunRecord, step StepRecord, before, after State) error {
	beforeSnapshot, err := SnapshotFromState(before)
	if err != nil {
		return err
	}
	afterSnapshot, err := SnapshotFromState(after)
	if err != nil {
		return err
	}

	changes, err := r.StateCodec.Diff(beforeSnapshot, afterSnapshot)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		return nil
	}
	logger.Debug("state diff computed",
		zap.String("run_id", run.RunID),
		zap.String("step_id", step.StepID),
		zap.String("node_id", step.NodeID),
		zap.Int("change_count", len(changes)),
	)

	return r.publishEvent(ctx, run, step.StepID, step.NodeID, EventStateChanged, map[string]any{
		"changes": changes,
	})
}

func (r *GraphRunner) pauseRun(ctx context.Context, run RunRecord, state State, step StepRecord, checkpointID string, hit *BreakpointHit) (RunRecord, State, error) {
	now := r.now()
	run.Status = RunStatusPaused
	run.PauseRequested = false
	run.LastCheckpointID = checkpointID
	run.UpdatedAt = now
	run.FinishedAt = nil
	step.Status = StepStatusPaused
	step.UpdatedAt = now
	if err := r.ExecutionStore.UpdateStep(ctx, step); err != nil {
		return RunRecord{}, state, err
	}
	if err := r.ExecutionStore.UpdateRun(ctx, run); err != nil {
		return RunRecord{}, state, err
	}
	fields := append(runLogFields(run), stepLogFields(step)...)
	fields = append(fields, stateSummaryFields(state)...)
	if hit != nil {
		fields = append(fields,
			zap.String("breakpoint_id", hit.BreakpointID),
			zap.String("breakpoint_stage", hit.Stage),
		)
	}
	logger.Info("run paused", fields...)
	if hit != nil {
		if err := r.publishEvent(ctx, run, step.StepID, step.NodeID, EventBreakpointHit, hit); err != nil {
			return RunRecord{}, state, err
		}
	}
	if err := r.publishEvent(ctx, run, step.StepID, step.NodeID, EventRunPaused, nil); err != nil {
		return RunRecord{}, state, err
	}
	return run, state, nil
}

func (r *GraphRunner) failRun(ctx context.Context, run RunRecord, state State, code string, message string) (RunRecord, State, error) {
	now := r.now()
	run.Status = RunStatusFailed
	run.ErrorCode = code
	run.ErrorMessage = message
	run.UpdatedAt = now
	run.FinishedAt = &now
	if err := r.ExecutionStore.UpdateRun(ctx, run); err != nil {
		return RunRecord{}, state, err
	}
	logger.Error("run failed", append(runLogFields(run), stateSummaryFields(state)...)...)
	if err := r.publishEvent(ctx, run, "", run.CurrentNodeID, EventRunFailed, map[string]any{
		"error_code":    code,
		"error_message": message,
	}); err != nil {
		return RunRecord{}, state, err
	}
	return run, state, errors.New(message)
}

func (r *GraphRunner) resumeTarget(checkpoint CheckpointRecord, state State) (string, *breakpointSkip, error) {
	nodeID, err := r.runnerGraph().ResolveNodeID(checkpoint.NodeID)
	if err != nil {
		return "", nil, err
	}
	switch checkpoint.Stage {
	case CheckpointBeforeNode:
		return nodeID, &breakpointSkip{NodeID: checkpoint.NodeID, Stage: string(CheckpointBeforeNode)}, nil
	case CheckpointAfterNode:
		nextNodeID, err := r.runnerGraph().ResolveNextNode(nodeID, state)
		return nextNodeID, nil, err
	default:
		return "", nil, fmt.Errorf("unsupported checkpoint stage %q", checkpoint.Stage)
	}
}

func (r *GraphRunner) resolveNextNode(currentName string, state State) (string, error) {
	graph := r.runnerGraph()
	if graph == nil {
		return "", errors.New("graph runner graph is nil")
	}
	return graph.ResolveNextNode(currentName, state)
}

func (r *GraphRunner) notifyListeners(ctx context.Context, event langgraph.NodeEvent, nodeID string, state State, err error) {
	graph := r.runnerGraph()
	if graph == nil {
		return
	}
	graph.NotifyListeners(ctx, event, nodeID, state, err)
}

func (r *GraphRunner) matchBreakpoint(nodeID string, stage string, skip *breakpointSkip) *BreakpointHit {
	if skip != nil && !skip.Consumed && skip.NodeID == nodeID && skip.Stage == stage {
		skip.Consumed = true
		return nil
	}
	for _, breakpoint := range r.Breakpoints {
		if !breakpoint.Enabled {
			continue
		}
		if breakpoint.NodeID != nodeID || breakpoint.Stage != stage {
			continue
		}
		return &BreakpointHit{
			BreakpointID: breakpoint.ID,
			NodeID:       breakpoint.NodeID,
			Stage:        breakpoint.Stage,
			HitAt:        r.now(),
		}
	}
	return nil
}

func (r *GraphRunner) publishEvent(ctx context.Context, run RunRecord, stepID string, nodeID string, eventType EventType, payload any) error {
	if r.EventSink == nil {
		return nil
	}
	var raw json.RawMessage
	if payload != nil {
		bytes, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		raw = bytes
	}
	return r.EventSink.Publish(ctx, Event{
		ID:        newRunnerID(),
		RunID:     run.RunID,
		StepID:    stepID,
		NodeID:    nodeID,
		Type:      eventType,
		Timestamp: r.now(),
		Payload:   raw,
	})
}

func (r *GraphRunner) validate() error {
	if r == nil {
		return errors.New("graph runner is nil")
	}
	graph := r.runnerGraph()
	if graph == nil {
		return errors.New("graph runner graph is nil")
	}
	if err := graph.Validate(); err != nil {
		return err
	}
	if r.ExecutionStore == nil {
		return errors.New("graph runner execution store is nil")
	}
	if r.CheckpointStore == nil {
		return errors.New("graph runner checkpoint store is nil")
	}
	if r.StateCodec == nil {
		return errors.New("graph runner state codec is nil")
	}
	if r.EventSink == nil {
		return errors.New("graph runner event sink is nil")
	}
	return nil
}

func (r *GraphRunner) recordArtifact(ctx context.Context, artifact Artifact) (ArtifactRef, error) {
	if r == nil || r.ArtifactStore == nil {
		return ArtifactRef{}, ErrArtifactRecorderUnavailable
	}

	metadata, _ := RunnerMetadataFromContext(ctx)
	if artifact.RunID == "" {
		artifact.RunID = metadata.RunID
	}
	if artifact.StepID == "" {
		artifact.StepID = metadata.StepID
	}
	if artifact.NodeID == "" {
		artifact.NodeID = metadata.NodeID
	}
	if artifact.CreatedAt.IsZero() {
		artifact.CreatedAt = r.now()
	}
	if artifact.ID == "" {
		artifact.ID = newRunnerID()
	}

	ref, err := r.ArtifactStore.Save(ctx, artifact)
	if err != nil {
		return ArtifactRef{}, err
	}
	fields := append(artifactLogFields(ref), zap.Int("bytes", len(artifact.Data)))
	logger.Debug("artifact recorded", fields...)
	if artifact.RunID != "" {
		_ = r.publishEvent(ctx, RunRecord{RunID: artifact.RunID}, artifact.StepID, artifact.NodeID, EventArtifactCreated, map[string]any{
			"artifact_id": ref.ID,
			"type":        ref.Type,
			"mime_type":   ref.MIMEType,
			"location":    ref.Location,
		})
	}
	return ref, nil
}

func (r *GraphRunner) validateRestoredCheckpoint(checkpoint RestoredCheckpoint) error {
	record := checkpoint.Record
	if codecName := strings.TrimSpace(record.StateCodec); codecName != "" && codecName != r.StateCodec.Name() {
		return fmt.Errorf("checkpoint %q uses state codec %q, runner configured for %q", record.CheckpointID, codecName, r.StateCodec.Name())
	}
	if version := strings.TrimSpace(record.StateVersion); version != "" && checkpoint.Snapshot.Version != "" && version != checkpoint.Snapshot.Version {
		return fmt.Errorf("checkpoint %q state version mismatch: record=%q snapshot=%q", record.CheckpointID, version, checkpoint.Snapshot.Version)
	}
	if record.RunID != "" && checkpoint.Runtime.RunID != "" && record.RunID != checkpoint.Runtime.RunID {
		return fmt.Errorf("checkpoint %q run mismatch: record=%q snapshot=%q", record.CheckpointID, record.RunID, checkpoint.Runtime.RunID)
	}
	if record.StepID != "" && checkpoint.Runtime.CurrentStepID != "" && record.StepID != checkpoint.Runtime.CurrentStepID {
		return fmt.Errorf("checkpoint %q step mismatch: record=%q snapshot=%q", record.CheckpointID, record.StepID, checkpoint.Runtime.CurrentStepID)
	}
	if record.NodeID != "" && checkpoint.Runtime.CurrentNodeID != "" && record.NodeID != checkpoint.Runtime.CurrentNodeID {
		return fmt.Errorf("checkpoint %q node mismatch: record=%q snapshot=%q", record.CheckpointID, record.NodeID, checkpoint.Runtime.CurrentNodeID)
	}
	return nil
}

func (r *GraphRunner) graphID() string {
	if text := strings.TrimSpace(r.GraphID); text != "" {
		return text
	}
	return "graph"
}

func (r *GraphRunner) graphVersion() string {
	if text := strings.TrimSpace(r.GraphVersion); text != "" {
		return text
	}
	return DefaultGraphVersion
}

func (r *GraphRunner) nodeName(nodeID string) string {
	graph := r.runnerGraph()
	if graph == nil {
		return nodeID
	}
	return graph.NodeName(nodeID)
}

func (r *GraphRunner) entryPointID() string {
	graph := r.runnerGraph()
	if graph == nil {
		return ""
	}
	return graph.EntryPointID()
}

func (r *GraphRunner) runnerGraph() RunnerGraph {
	if r == nil {
		return nil
	}
	return r.graph
}

func (r *GraphRunner) now() time.Time {
	if r != nil && r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func newRunnerID() string {
	return uuid.NewString()
}

type breakpointSkip struct {
	NodeID   string
	Stage    string
	Consumed bool
}
