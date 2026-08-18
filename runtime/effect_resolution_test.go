package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/core"
)

func TestEffectResolutionTransitionIsForwardOnly(t *testing.T) {
	store := NewMemoryExecutionStore()
	run := RunRecord{RunID: "run-effect-resolution", Status: RunStatusFailed}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	step := StepRecord{
		StepID: "step-effect-resolution", RunID: run.RunID, TaskID: "task", NodeID: "writer",
		OperationKey: "operation-effect-resolution", EffectClass: core.EffectNonIdempotentWrite,
		EffectStatus: core.EffectUnknown, Status: StepStatusFailed,
	}
	if err := store.AppendStep(context.Background(), step); err != nil {
		t.Fatal(err)
	}
	requestedAt := time.Now().UTC()
	step.EffectResolution = &EffectResolution{
		ID: "resolution-1", AttemptID: "attempt-1", Action: EffectResolutionConfirmNotApplied,
		Status: EffectResolutionIntent, Actor: "operator-1", Reason: "provider rejected the write", RequestedAt: requestedAt,
	}
	if err := store.UpdateStep(context.Background(), step); err != nil {
		t.Fatal(err)
	}
	resolvedAt := requestedAt.Add(time.Second)
	step.EffectResolution.Status = EffectResolutionSucceeded
	step.EffectResolution.ResolvedAt = &resolvedAt
	step.EffectStatus = core.EffectNotApplied
	if err := store.UpdateStep(context.Background(), step); err != nil {
		t.Fatal(err)
	}

	changed := cloneStepRecord(step)
	changed.EffectResolution.Reason = "changed reason"
	if err := store.UpdateStep(context.Background(), changed); err == nil {
		t.Fatal("UpdateStep() changed a terminal effect resolution identity")
	}
	rolledBack := cloneStepRecord(step)
	rolledBack.EffectResolution.Status = EffectResolutionIntent
	rolledBack.EffectResolution.ResolvedAt = nil
	rolledBack.EffectStatus = core.EffectUnknown
	if err := store.UpdateStep(context.Background(), rolledBack); err == nil {
		t.Fatal("UpdateStep() rolled a terminal effect resolution backward")
	}
}
