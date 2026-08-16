package graph

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/registry"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

func TestExecutionPolicyLimitsLoopAndPublishesInspectionEvent(t *testing.T) {
	workflow := NewGraph(nil)
	mustAddNode(t, workflow, "loop", func(context.Context, *state.Access) error { return nil })
	if err := workflow.SetEntryPoint("loop"); err != nil {
		t.Fatal(err)
	}
	condition := registry.NewEdgeCondition(dsl.GraphConditionSpec{Type: "test"}, func(context.Context, *state.State) (registry.RouteDecision, error) {
		return registry.RouteDecision{Matched: true, Reason: "loop"}, nil
	})
	if err := workflow.AddConditionalEdge("loop", "loop", condition); err != nil {
		t.Fatal(err)
	}
	if err := workflow.AddEdge("loop", EndNodeRef); err != nil {
		t.Fatal(err)
	}
	policy := workflow.ExecutionPolicy()
	policy.Limits.MaxSuperSteps = 2
	if err := workflow.SetExecutionPolicy(policy); err != nil {
		t.Fatal(err)
	}

	directory := t.TempDir()
	runner := mustNewGraphRunner(t, workflow,
		fruntime.NewFileExecutionStore(directory),
		fruntime.NewFileCheckpointStore(directory),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(directory),
	)
	run, _, err := runner.Start(context.Background(), state.NewState())
	if err == nil || run.Status != fruntime.RunStatusFailed || run.ErrorCode != string(core.ErrorResourceExhausted) {
		t.Fatalf("run = %#v, error = %v", run, err)
	}
	events, err := runner.ListEvents(run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == fruntime.EventRunLimitExceeded && strings.Contains(string(event.Payload), `"kind":"super_steps"`) {
			return
		}
	}
	t.Fatalf("limit event missing: %#v", events)
}

func TestExecutionPolicyBudgetSurvivesResume(t *testing.T) {
	workflow := NewGraph(nil)
	mustAddNode(t, workflow, "first", func(context.Context, *state.Access) error { return nil })
	mustAddNode(t, workflow, "second", func(context.Context, *state.Access) error { return nil })
	if err := workflow.SetEntryPoint("first"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.AddEdge("first", "second"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.AddEdge("second", EndNodeRef); err != nil {
		t.Fatal(err)
	}
	policy := workflow.ExecutionPolicy()
	policy.Limits.MaxNodeExecutions = 1
	if err := workflow.SetExecutionPolicy(policy); err != nil {
		t.Fatal(err)
	}

	directory := t.TempDir()
	runner := mustNewGraphRunner(t, workflow,
		fruntime.NewFileExecutionStore(directory),
		fruntime.NewFileCheckpointStore(directory),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(directory),
		fruntime.WithBreakpoints(fruntime.Breakpoint{
			ID: "after-first", NodeID: "first", Stage: string(fruntime.CheckpointAfterNode), Enabled: true,
		}),
	)
	pausedRun, _, err := runner.Start(context.Background(), state.NewState())
	if err != nil || pausedRun.Status != fruntime.RunStatusPaused {
		t.Fatalf("paused run = %#v, error = %v", pausedRun, err)
	}
	restored, err := runner.LoadCheckpointState(context.Background(), pausedRun.LastCheckpointID)
	if err != nil {
		t.Fatal(err)
	}
	budget, ok, err := fruntime.LoadGraphExecutionBudget(restored.Business)
	if err != nil {
		t.Fatalf("LoadGraphExecutionBudget() error = %v", err)
	}
	if !ok || budget.SuperSteps != 1 || budget.NodeExecutions != 1 || budget.ElapsedWallTime <= 0 {
		t.Fatalf("restored budget = %#v ok=%v", budget, ok)
	}

	resumedRun, _, err := runner.Resume(context.Background(), pausedRun.RunID, nil)
	if err == nil || resumedRun.Status != fruntime.RunStatusFailed || resumedRun.ErrorCode != string(core.ErrorResourceExhausted) {
		t.Fatalf("resumed run = %#v, error = %v", resumedRun, err)
	}
	if !strings.Contains(err.Error(), "node execution limit") {
		t.Fatalf("resume error = %v", err)
	}
}

func TestExecutionPolicyBudgetIncludesInterruptedActiveTime(t *testing.T) {
	workflow := NewGraph(nil)
	started := make(chan struct{})
	mustAddNode(t, workflow, "work", func(ctx context.Context, _ *state.Access) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	if err := workflow.SetEntryPoint("work"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.AddEdge("work", EndNodeRef); err != nil {
		t.Fatal(err)
	}
	policy := workflow.ExecutionPolicy()
	policy.Limits.MaxWallTime = time.Second
	if err := workflow.SetExecutionPolicy(policy); err != nil {
		t.Fatal(err)
	}

	directory := t.TempDir()
	runner := mustNewGraphRunner(t, workflow,
		fruntime.NewFileExecutionStore(directory),
		fruntime.NewFileCheckpointStore(directory),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(directory),
	)
	run, done, err := runner.StartAsync(context.Background(), state.NewState())
	if err != nil {
		t.Fatal(err)
	}
	<-started
	time.Sleep(25 * time.Millisecond)
	if err := runner.Pause(context.Background(), run.RunID); err != nil {
		t.Fatal(err)
	}
	<-done
	pausedRun, err := runner.GetRun(context.Background(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if pausedRun.Status != fruntime.RunStatusPaused {
		t.Fatalf("run status = %q, want paused", pausedRun.Status)
	}
	restored, err := runner.LoadCheckpointState(context.Background(), pausedRun.LastCheckpointID)
	if err != nil {
		t.Fatal(err)
	}
	budget, ok, err := fruntime.LoadGraphExecutionBudget(restored.Business)
	if err != nil {
		t.Fatalf("LoadGraphExecutionBudget() error = %v", err)
	}
	if !ok || budget.SuperSteps != 1 || budget.NodeExecutions != 1 || budget.ElapsedWallTime < 20*time.Millisecond {
		t.Fatalf("interrupted execution budget = %#v ok=%v", budget, ok)
	}
}

func TestExecutionPolicyRoundTripsGraphAndNodeOverrides(t *testing.T) {
	workflow := NewGraph(nil)
	mustAddNode(t, workflow, "target", func(context.Context, *state.Access) error { return nil })
	if err := workflow.SetEntryPoint("target"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.AddEdge("target", EndNodeRef); err != nil {
		t.Fatal(err)
	}
	policy := workflow.ExecutionPolicy()
	policy.Limits.MaxSuperSteps = 17
	policy.NodeDefaults.Timeout = 11 * time.Second
	if err := workflow.SetExecutionPolicy(policy); err != nil {
		t.Fatal(err)
	}
	if err := workflow.SetNodeSpec(dsl.GraphNodeSpec{
		ID: "target", Name: "target", Type: "test", State: map[string]dsl.StateBinding{},
		Policy: &dsl.ExecutionPolicy{
			Timeout: "3s",
			Retry: &dsl.RetryPolicy{
				MaxAttempts:              2,
				NonRetryableErrorClasses: []string{string(core.ErrorTimeout)},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	definition, err := workflow.Definition()
	if err != nil {
		t.Fatal(err)
	}
	if definition.Policy == nil || definition.Policy.Limits.MaxSuperSteps != 17 || definition.Policy.NodeDefaults.Timeout != "11s" {
		t.Fatalf("graph policy = %#v", definition.Policy)
	}
	if definition.Nodes[0].Policy == nil || definition.Nodes[0].Policy.Timeout != "3s" || definition.Nodes[0].Policy.Retry.MaxAttempts != 2 {
		t.Fatalf("node policy = %#v", definition.Nodes[0].Policy)
	}
	if definition.Nodes[0].Policy.Retry.InitialInterval != "" {
		t.Fatalf("node policy serialized inherited retry fields: %#v", definition.Nodes[0].Policy)
	}
	nodePolicy := workflow.nodeExecutionPolicy("target")
	if retryable(nodePolicy.Retry, core.NewExecutionError(core.ErrorTimeout, "timeout", nil, nil)) {
		t.Fatalf("node non-retryable override did not remove inherited retry class: %#v", nodePolicy.Retry)
	}
}

func TestExecutionPolicyRetryableOverrideRemovesInheritedNonRetryableClass(t *testing.T) {
	base := fruntime.DefaultGraphExecutionPolicy().NodeDefaults
	policy, err := executionPolicyFromDSL(&dsl.ExecutionPolicy{
		Retry: &dsl.RetryPolicy{
			RetryableErrorClasses: []string{string(core.ErrorCanceled)},
		},
	}, base)
	if err != nil {
		t.Fatal(err)
	}
	if !retryable(policy.Retry, core.NewExecutionError(core.ErrorCanceled, "retry cancellation", nil, nil)) {
		t.Fatalf("retryable override did not remove inherited non-retryable class: %#v", policy.Retry)
	}

	_, err = executionPolicyFromDSL(&dsl.ExecutionPolicy{
		Retry: &dsl.RetryPolicy{
			RetryableErrorClasses:    []string{string(core.ErrorCanceled)},
			NonRetryableErrorClasses: []string{string(core.ErrorCanceled)},
		},
	}, base)
	if err == nil || !strings.Contains(err.Error(), "both retryable and non-retryable") {
		t.Fatalf("explicit conflicting classes error = %v", err)
	}
}

func TestExecutionPolicyDoesNotRetryAbandonedSchedulerTimeout(t *testing.T) {
	policy := fruntime.DefaultGraphExecutionPolicy().NodeDefaults
	if !retryable(policy.Retry, core.NewExecutionError(core.ErrorTimeout, "node timeout", nil, nil)) {
		t.Fatalf("completed node timeout is not retryable: %#v", policy.Retry)
	}
	abandoned := core.NewExecutionError(core.ErrorTimeout, "scheduler timeout", nil, map[string]any{"attempt_abandoned": true})
	if retryable(policy.Retry, abandoned) {
		t.Fatalf("abandoned scheduler timeout is retryable: %#v", policy.Retry)
	}

	override, err := executionPolicyFromDSL(&dsl.ExecutionPolicy{Retry: &dsl.RetryPolicy{
		RetryableErrorClasses: []string{string(core.ErrorTimeout)},
	}}, policy)
	if err != nil {
		t.Fatalf("retryable node timeout override: %v", err)
	}
	if !retryable(override.Retry, core.NewExecutionError(core.ErrorTimeout, "node timeout", nil, nil)) || retryable(override.Retry, abandoned) {
		t.Fatalf("timeout override did not preserve scheduler abandonment boundary: %#v", override.Retry)
	}
}

func TestExecutionPolicyGraphDefaultsRefreshPartialNodeOverrides(t *testing.T) {
	workflow := NewGraph(nil)
	mustAddNode(t, workflow, "target", func(context.Context, *state.Access) error { return nil })
	if err := workflow.SetNodeSpec(dsl.GraphNodeSpec{
		ID: "target", Name: "target", Type: "test", State: map[string]dsl.StateBinding{},
		Policy: &dsl.ExecutionPolicy{Timeout: "3s"},
	}); err != nil {
		t.Fatal(err)
	}
	policy := workflow.ExecutionPolicy()
	policy.NodeDefaults.Retry.MaxAttempts = 4
	if err := workflow.SetExecutionPolicy(policy); err != nil {
		t.Fatal(err)
	}
	nodePolicy := workflow.nodeExecutionPolicy("target")
	if nodePolicy.Timeout != 3*time.Second || nodePolicy.Retry.MaxAttempts != 4 {
		t.Fatalf("node policy did not refresh inherited defaults: %#v", nodePolicy)
	}
	nodePolicy.Timeout = 7 * time.Second
	if err := workflow.SetNodeExecutionPolicy("target", nodePolicy); err != nil {
		t.Fatal(err)
	}
	policy.NodeDefaults.Timeout = 9 * time.Second
	if err := workflow.SetExecutionPolicy(policy); err != nil {
		t.Fatal(err)
	}
	if got := workflow.nodeExecutionPolicy("target").Timeout; got != 7*time.Second {
		t.Fatalf("direct node policy was replaced by stale DSL override: %s", got)
	}
}

func TestExecutionPolicyUsesStructuredRetryAndNodeTimeout(t *testing.T) {
	workflow := NewGraph(nil)
	var attempts atomic.Int32
	mustAddNode(t, workflow, "target", func(ctx context.Context, access *state.Access) error {
		if attempts.Add(1) == 1 {
			return core.NewExecutionError(core.ErrorUnavailable, "provider unavailable", nil, nil)
		}
		return access.SetAny(state.Shared("done"), true)
	})
	if err := workflow.SetEntryPoint("target"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.AddEdge("target", EndNodeRef); err != nil {
		t.Fatal(err)
	}
	policy := workflow.ExecutionPolicy()
	policy.NodeDefaults.Timeout = time.Second
	policy.NodeDefaults.Retry.MaxAttempts = 2
	policy.NodeDefaults.Retry.InitialInterval = time.Millisecond
	policy.NodeDefaults.Retry.MaxInterval = time.Millisecond
	policy.NodeDefaults.Retry.Jitter = 0
	if err := workflow.SetExecutionPolicy(policy); err != nil {
		t.Fatal(err)
	}

	directory := t.TempDir()
	runner := mustNewGraphRunner(t, workflow,
		fruntime.NewFileExecutionStore(directory),
		fruntime.NewFileCheckpointStore(directory),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(directory),
	)
	run, finalState, err := runner.Start(context.Background(), state.NewState())
	if err != nil || run.Status != fruntime.RunStatusCompleted || attempts.Load() != 2 {
		t.Fatalf("run = %#v, attempts = %d, error = %v", run, attempts.Load(), err)
	}
	if done, ok := state.NewAccess(finalState).ReadAny(state.Shared("done")); !ok || done != true {
		t.Fatalf("final state = %#v", finalState)
	}
	events, err := runner.ListEvents(run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == fruntime.EventNodeRetry && strings.Contains(string(event.Payload), string(core.ErrorUnavailable)) {
			return
		}
	}
	t.Fatalf("retry event missing: %#v", events)
}

func TestExecutionPolicyConvertsNodePanicToRunFailure(t *testing.T) {
	workflow := NewGraph(nil)
	mustAddNode(t, workflow, "panic", func(context.Context, *state.Access) error {
		panic("boom")
	})
	if err := workflow.SetEntryPoint("panic"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.AddEdge("panic", EndNodeRef); err != nil {
		t.Fatal(err)
	}

	directory := t.TempDir()
	runner := mustNewGraphRunner(t, workflow,
		fruntime.NewFileExecutionStore(directory),
		fruntime.NewFileCheckpointStore(directory),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(directory),
	)
	run, _, err := runner.Start(context.Background(), state.NewState())
	if err == nil || run.Status != fruntime.RunStatusFailed || !strings.Contains(err.Error(), "panic in node panic: boom") {
		t.Fatalf("run = %#v, error = %v", run, err)
	}
	steps, listErr := runner.ListSteps(context.Background(), run.RunID)
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(steps) != 1 || steps[0].Status != fruntime.StepStatusFailed {
		t.Fatalf("steps = %#v", steps)
	}
}

func TestExecutionPolicyWallTimeInterruptsQueuedFanOut(t *testing.T) {
	workflow := NewGraph(nil)
	mustAddNode(t, workflow, "router", func(context.Context, *state.Access) error { return nil })
	release := make(chan struct{})
	for _, nodeID := range []string{"a", "b", "c"} {
		mustAddNode(t, workflow, nodeID, func(context.Context, *state.Access) error {
			<-release
			return nil
		})
	}
	if err := workflow.SetEntryPoint("router"); err != nil {
		t.Fatal(err)
	}
	for _, nodeID := range []string{"a", "b", "c"} {
		if err := workflow.AddEdge("router", nodeID); err != nil {
			t.Fatal(err)
		}
		if err := workflow.AddEdge(nodeID, EndNodeRef); err != nil {
			t.Fatal(err)
		}
	}
	policy := workflow.ExecutionPolicy()
	policy.Limits.MaxConcurrentNodes = 1
	policy.Limits.MaxWallTime = 30 * time.Millisecond
	policy.NodeDefaults.Timeout = time.Second
	if err := workflow.SetExecutionPolicy(policy); err != nil {
		t.Fatal(err)
	}

	startedAt := time.Now()
	_, err := workflow.Run(context.Background(), state.NewState())
	close(release)
	if err == nil || core.ClassifyError(err) != core.ErrorTimeout {
		t.Fatalf("run error = %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed > 300*time.Millisecond {
		t.Fatalf("wall-time enforcement took %s", elapsed)
	}
}

func TestExecutionPolicyNodeTimeoutRejectsLateResult(t *testing.T) {
	workflow := NewGraph(nil)
	release := make(chan struct{})
	mustAddNode(t, workflow, "slow", func(context.Context, *state.Access) error {
		<-release
		return nil
	})
	if err := workflow.SetEntryPoint("slow"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.AddEdge("slow", EndNodeRef); err != nil {
		t.Fatal(err)
	}
	policy := workflow.ExecutionPolicy()
	policy.NodeDefaults.Timeout = 20 * time.Millisecond
	policy.NodeDefaults.Retry.MaxAttempts = 1
	if err := workflow.SetExecutionPolicy(policy); err != nil {
		t.Fatal(err)
	}

	directory := t.TempDir()
	runner := mustNewGraphRunner(t, workflow,
		fruntime.NewFileExecutionStore(directory),
		fruntime.NewFileCheckpointStore(directory),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(directory),
	)
	run, _, err := runner.Start(context.Background(), state.NewState())
	close(release)
	if err == nil || run.Status != fruntime.RunStatusFailed || run.ErrorCode != string(core.ErrorTimeout) {
		t.Fatalf("run = %#v, error = %v", run, err)
	}
	steps, err := runner.ListSteps(context.Background(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].Status != fruntime.StepStatusFailed || steps[0].CheckpointAfterID != "" {
		t.Fatalf("steps = %#v", steps)
	}
}

func TestExecutionPolicyCommittedAttemptWinsDeadlineBeforeResultDelivery(t *testing.T) {
	workflow := NewGraph(nil)
	mustAddNode(t, workflow, "work", func(_ context.Context, access *state.Access) error {
		return access.SetAny(state.Shared("finished"), true)
	})
	if err := workflow.SetEntryPoint("work"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.SetFinishPoint("work"); err != nil {
		t.Fatal(err)
	}
	policy := workflow.ExecutionPolicy()
	policy.NodeDefaults.Timeout = 50 * time.Millisecond
	policy.NodeDefaults.Retry.MaxAttempts = 1
	if err := workflow.SetExecutionPolicy(policy); err != nil {
		t.Fatal(err)
	}

	directory := t.TempDir()
	runtimeStore, err := fruntime.NewFileRuntimeStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	runner := mustNewGraphRunner(t, workflow, runtimeStore, runtimeStore, state.NewJSONStateCodec(""), runtimeStore)
	committed := make(chan string, 1)
	releaseResult := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(releaseResult)
		}
	}()
	ctx := fruntime.WithRunnerEventObserver(context.Background(), fruntime.EventObserverFunc(func(_ context.Context, event fruntime.Event) error {
		if event.Type == fruntime.EventNodeFinished && event.NodeID == "work" {
			committed <- event.RunID
			<-releaseResult
		}
		return nil
	}))
	done := make(chan runnerResult, 1)
	go func() {
		run, finalState, runErr := runner.Start(ctx, state.NewState())
		done <- runnerResult{run: run, state: finalState, err: runErr}
	}()

	var runID string
	select {
	case runID = <-committed:
	case <-time.After(5 * time.Second):
		t.Fatal("node result was not committed")
	}
	steps, err := runner.ListSteps(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].Status != fruntime.StepStatusSucceeded || steps[0].CheckpointAfterID == "" {
		t.Fatalf("committed steps = %#v", steps)
	}
	select {
	case result := <-done:
		t.Fatalf("committed attempt was abandoned at its deadline: run=%#v error=%v", result.run, result.err)
	case <-time.After(150 * time.Millisecond):
	}

	close(releaseResult)
	released = true
	result := waitForRunnerResult(t, done)
	if result.err != nil || result.run.Status != fruntime.RunStatusCompleted {
		t.Fatalf("run = %#v, error = %v", result.run, result.err)
	}
	finished, _ := state.ReadPath(result.state, "shared.finished")
	if finished != true {
		t.Fatalf("final state finished = %#v", finished)
	}
}

func TestExecutionPolicyAbandonedInvalidOutputDoesNotPublishContractViolation(t *testing.T) {
	workflow := NewGraph(nil)
	release := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	lateResult := make(chan struct{})
	lateEventErr := make(chan error, 1)
	lateArtifactErr := make(chan error, 1)
	mustAddResultNode(t, workflow, "slow", func(nodeCtx core.Context, _ *state.Access) (core.NodeResult, error) {
		<-release
		lateEventErr <- fruntime.PublishRunnerContextEvent(nodeCtx, fruntime.EventType("late.inline"), nil)
		_, artifactErr := fruntime.SaveArtifact(nodeCtx, fruntime.Artifact{Type: "late.inline", Data: []byte("late")})
		lateArtifactErr <- artifactErr
		close(lateResult)
		return core.NodeResult{Patch: state.NewPatch(state.PatchOp{
			Kind: state.OpSet, Path: state.Shared("forbidden"), Value: true,
		}), Events: []core.EventDraft{{Type: "late.invalid"}}, Artifacts: []core.ArtifactDraft{{Type: "late.invalid", MIMEType: "text/plain", Data: []byte("late")}}}, nil
	})
	if err := workflow.SetEntryPoint("slow"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.SetFinishPoint("slow"); err != nil {
		t.Fatal(err)
	}
	workflow.setNodeContracts(map[string]state.Contract{
		"slow": state.NewContract(state.FieldAccess{Path: state.Shared("allowed"), Mode: state.AccessWrite}),
	})
	policy := workflow.ExecutionPolicy()
	policy.NodeDefaults.Timeout = 20 * time.Millisecond
	policy.NodeDefaults.Retry.MaxAttempts = 1
	if err := workflow.SetExecutionPolicy(policy); err != nil {
		t.Fatal(err)
	}

	runtimeStore := fruntime.NewMemoryRuntimeStore()
	artifactStore := fruntime.NewMemoryArtifactStore()
	runner := mustNewGraphRunner(t, workflow, runtimeStore, runtimeStore, state.NewJSONStateCodec(""), runtimeStore,
		fruntime.WithArtifactStore(artifactStore))
	run, _, err := runner.Start(context.Background(), state.NewState())
	if err == nil || run.Status != fruntime.RunStatusFailed || run.ErrorCode != string(core.ErrorTimeout) {
		t.Fatalf("run = %#v, error = %v", run, err)
	}
	close(release)
	released = true
	select {
	case <-lateResult:
	case <-time.After(time.Second):
		t.Fatal("late invalid result did not return")
	}
	if eventErr := <-lateEventErr; eventErr == nil {
		t.Fatal("abandoned attempt published an inline event")
	}
	if artifactErr := <-lateArtifactErr; artifactErr == nil {
		t.Fatal("abandoned attempt recorded an inline artifact")
	}
	time.Sleep(100 * time.Millisecond)

	persistedRun, err := runner.GetRun(context.Background(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if persistedRun.Status != fruntime.RunStatusFailed || persistedRun.ErrorCode != string(core.ErrorTimeout) {
		t.Fatalf("late invalid result changed run = %#v", persistedRun)
	}
	events, err := runner.ListEvents(run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == fruntime.EventContractViolation || event.Type == fruntime.EventType("late.invalid") || event.Type == fruntime.EventType("late.inline") {
			t.Fatalf("abandoned result published output event: %#v", event)
		}
	}
	artifacts, err := artifactStore.List(context.Background(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range artifacts {
		if artifact.Type == "late.invalid" || artifact.Type == "late.inline" || artifact.Type == "contract.output_patch" || artifact.Type == "contract.merged_state" {
			t.Fatalf("abandoned result recorded output artifact: %#v", artifact)
		}
	}
	steps, err := runner.ListSteps(context.Background(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].Status != fruntime.StepStatusFailed || steps[0].CheckpointAfterID != "" {
		t.Fatalf("steps = %#v", steps)
	}
}

func TestExecutionPolicyParallelTimeoutKeepsTaskSpecificStepError(t *testing.T) {
	workflow := NewGraph(nil)
	secondAttemptStarted := make(chan struct{})
	release := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	var attempts atomic.Int32
	mustAddNode(t, workflow, "router", func(context.Context, *state.Access) error { return nil })
	mustAddNode(t, workflow, "slow", func(context.Context, *state.Access) error {
		if attempts.Add(1) == 1 {
			return core.NewExecutionError(core.ErrorUnavailable, "first attempt unavailable", nil, nil)
		}
		close(secondAttemptStarted)
		<-release
		return nil
	})
	mustAddNode(t, workflow, "failed", func(context.Context, *state.Access) error {
		<-secondAttemptStarted
		return errors.New("other branch failed")
	})
	if err := workflow.SetEntryPoint("router"); err != nil {
		t.Fatal(err)
	}
	for _, edge := range [][2]string{{"router", "slow"}, {"router", "failed"}, {"slow", EndNodeRef}, {"failed", EndNodeRef}} {
		if err := workflow.AddEdge(edge[0], edge[1]); err != nil {
			t.Fatal(err)
		}
	}
	slowPolicy := workflow.nodeExecutionPolicy("slow")
	slowPolicy.Timeout = 20 * time.Millisecond
	slowPolicy.Retry.MaxAttempts = 2
	slowPolicy.Retry.InitialInterval = 0
	slowPolicy.Retry.MaxInterval = 0
	slowPolicy.Retry.BackoffMultiplier = 1
	slowPolicy.Retry.Jitter = 0
	if err := workflow.SetNodeExecutionPolicy("slow", slowPolicy); err != nil {
		t.Fatal(err)
	}

	runtimeStore := fruntime.NewMemoryRuntimeStore()
	runner := mustNewGraphRunner(t, workflow, runtimeStore, runtimeStore, state.NewJSONStateCodec(""), runtimeStore)
	run, _, err := runner.Start(context.Background(), state.NewState())
	if err == nil || run.Status != fruntime.RunStatusFailed {
		t.Fatalf("run = %#v, error = %v", run, err)
	}
	close(release)
	released = true
	steps, err := runner.ListSteps(context.Background(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	byNode := map[string]fruntime.StepRecord{}
	for _, step := range steps {
		byNode[step.NodeID] = step
	}
	if slow := byNode["slow"]; slow.Status != fruntime.StepStatusFailed || slow.ErrorCode != string(core.ErrorTimeout) || !strings.Contains(slow.ErrorMessage, "timed out") || strings.Contains(slow.ErrorMessage, "other branch") || strings.Contains(slow.ErrorMessage, "first attempt") {
		t.Fatalf("slow step = %#v", slow)
	}
	if failed := byNode["failed"]; failed.Status != fruntime.StepStatusFailed || !strings.Contains(failed.ErrorMessage, "other branch failed") || strings.Contains(failed.ErrorMessage, "timed out") {
		t.Fatalf("failed step = %#v", failed)
	}
}

func TestExecutionPolicyTimeoutDoesNotWaitForUncooperativeAttempt(t *testing.T) {
	workflow := NewGraph(nil)
	started := make(chan struct{}, 1)
	releaseFirst := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(releaseFirst)
		}
	}()
	var attempts atomic.Int32
	mustAddNode(t, workflow, "slow", func(context.Context, *state.Access) error {
		attempts.Add(1)
		started <- struct{}{}
		<-releaseFirst
		return nil
	})
	if err := workflow.SetEntryPoint("slow"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.AddEdge("slow", EndNodeRef); err != nil {
		t.Fatal(err)
	}
	policy := workflow.ExecutionPolicy()
	policy.Limits.MaxWallTime = 2 * time.Second
	policy.NodeDefaults.Timeout = 20 * time.Millisecond
	policy.NodeDefaults.Retry.MaxAttempts = 2
	policy.NodeDefaults.Retry.InitialInterval = time.Millisecond
	policy.NodeDefaults.Retry.MaxInterval = time.Millisecond
	policy.NodeDefaults.Retry.Jitter = 0
	if err := workflow.SetExecutionPolicy(policy); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := workflow.Run(context.Background(), state.NewState())
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("attempt did not start")
	}
	select {
	case err := <-done:
		if err == nil || core.ClassifyError(err) != core.ErrorTimeout {
			t.Fatalf("run error = %v, want timeout", err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("node timeout waited for the uncooperative attempt")
	}
	if attempts.Load() != 1 {
		t.Fatalf("attempts = %d, want 1 non-overlapping attempt", attempts.Load())
	}
	close(releaseFirst)
	released = true
}

func TestExecutionPolicyParentCancellationDoesNotWaitForUncooperativeAttempt(t *testing.T) {
	workflow := NewGraph(nil)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	mustAddNode(t, workflow, "slow", func(context.Context, *state.Access) error {
		started <- struct{}{}
		<-release
		return nil
	})
	if err := workflow.SetEntryPoint("slow"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.AddEdge("slow", EndNodeRef); err != nil {
		t.Fatal(err)
	}
	policy := workflow.ExecutionPolicy()
	policy.Limits.MaxWallTime = 2 * time.Second
	policy.NodeDefaults.Timeout = time.Second
	if err := workflow.SetExecutionPolicy(policy); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := workflow.Run(ctx, state.NewState())
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("attempt did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("run error = %v, want context canceled", err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("parent cancellation waited for the uncooperative attempt")
	}
	close(release)
	released = true
}

func TestExecutionPolicyWallTimeStopsWaitingForTimedOutAttempt(t *testing.T) {
	workflow := NewGraph(nil)
	release := make(chan struct{})
	var attempts atomic.Int32
	mustAddNode(t, workflow, "slow", func(context.Context, *state.Access) error {
		attempts.Add(1)
		<-release
		return nil
	})
	if err := workflow.SetEntryPoint("slow"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.AddEdge("slow", EndNodeRef); err != nil {
		t.Fatal(err)
	}
	policy := workflow.ExecutionPolicy()
	policy.Limits.MaxWallTime = 80 * time.Millisecond
	policy.NodeDefaults.Timeout = 20 * time.Millisecond
	policy.NodeDefaults.Retry.MaxAttempts = 2
	policy.NodeDefaults.Retry.InitialInterval = time.Millisecond
	policy.NodeDefaults.Retry.MaxInterval = time.Millisecond
	policy.NodeDefaults.Retry.Jitter = 0
	if err := workflow.SetExecutionPolicy(policy); err != nil {
		t.Fatal(err)
	}

	startedAt := time.Now()
	_, err := workflow.Run(context.Background(), state.NewState())
	close(release)
	if err == nil || core.ClassifyError(err) != core.ErrorTimeout {
		t.Fatalf("run error = %v", err)
	}
	if attempts.Load() != 1 {
		t.Fatalf("attempts = %d, want 1 non-overlapping attempt", attempts.Load())
	}
	if elapsed := time.Since(startedAt); elapsed > 500*time.Millisecond {
		t.Fatalf("wall-time enforcement took %s", elapsed)
	}
}

func TestExecutionPolicyPublishesRunBackpressure(t *testing.T) {
	workflow := NewGraph(nil)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	mustAddNode(t, workflow, "blocked", func(context.Context, *state.Access) error {
		started <- struct{}{}
		<-release
		return nil
	})
	if err := workflow.SetEntryPoint("blocked"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.AddEdge("blocked", EndNodeRef); err != nil {
		t.Fatal(err)
	}
	policy := workflow.ExecutionPolicy()
	policy.Limits.MaxConcurrentRuns = 1
	if err := workflow.SetExecutionPolicy(policy); err != nil {
		t.Fatal(err)
	}

	directory := t.TempDir()
	runner := mustNewGraphRunner(t, workflow,
		fruntime.NewFileExecutionStore(directory),
		fruntime.NewFileCheckpointStore(directory),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(directory),
	)
	first, firstDone, err := runner.StartAsync(context.Background(), state.NewState())
	if err != nil {
		t.Fatal(err)
	}
	<-started
	second, secondDone, err := runner.StartAsync(context.Background(), state.NewState())
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		events, listErr := runner.ListEvents(second.RunID)
		if listErr != nil {
			t.Fatal(listErr)
		}
		found := false
		for _, event := range events {
			if event.Type == fruntime.EventRunBackpressure && strings.Contains(string(event.Payload), `"scope":"run"`) {
				found = true
				break
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run backpressure event missing: first=%s second=%s", first.RunID, second.RunID)
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	<-firstDone
	<-secondDone
}

func TestConditionFailureIncludesStableIdentityAndStatePaths(t *testing.T) {
	workflow := NewGraph(nil)
	for _, nodeID := range []string{"router", "matched", "fallback"} {
		mustAddNode(t, workflow, nodeID, func(context.Context, *state.Access) error { return nil })
	}
	if err := workflow.SetEntryPoint("router"); err != nil {
		t.Fatal(err)
	}
	condition := registry.NewEdgeCondition(dsl.GraphConditionSpec{
		ID:   "route-ready",
		Type: "test",
		State: map[string]dsl.StateBinding{
			"route": {Path: "shared.route"},
		},
	}, func(context.Context, *state.State) (registry.RouteDecision, error) {
		return registry.RouteDecision{}, errors.New("route evaluation failed")
	})
	if err := workflow.AddConditionalEdge("router", "matched", condition); err != nil {
		t.Fatal(err)
	}
	if err := workflow.AddEdge("router", "fallback"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.AddEdge("matched", EndNodeRef); err != nil {
		t.Fatal(err)
	}
	if err := workflow.AddEdge("fallback", EndNodeRef); err != nil {
		t.Fatal(err)
	}

	directory := t.TempDir()
	runner := mustNewGraphRunner(t, workflow,
		fruntime.NewFileExecutionStore(directory),
		fruntime.NewFileCheckpointStore(directory),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(directory),
	)
	run, _, err := runner.Start(context.Background(), state.NewState())
	if err == nil || !strings.Contains(err.Error(), "route-ready") || !strings.Contains(err.Error(), "shared.route") {
		t.Fatalf("run = %#v, error = %v", run, err)
	}
	events, err := runner.ListEvents(run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == fruntime.EventConditionFailed && strings.Contains(string(event.Payload), `"condition_id":"route-ready"`) && strings.Contains(string(event.Payload), "shared.route") {
			return
		}
	}
	t.Fatalf("condition failure event missing: %#v", events)
}

type observerOpaqueMutable struct {
	values []string
}

func TestSchedulerObserversReceiveIsolatedMutableSnapshots(t *testing.T) {
	runnable := &scheduledRunnable{}
	currentState := state.FromShared(map[string]any{
		"nested": map[string]any{"value": "scheduler"},
	})
	completedTasks := []fruntime.GraphTask{{
		TaskID: "task-1",
		NodeID: "node-1",
		Input: state.NewPatch(state.PatchOp{
			Kind:  state.OpSet,
			Path:  state.Shared("input"),
			Value: map[string]any{"nested": map[string]any{"value": "scheduler"}},
		}),
		Failure: &fruntime.FailureContext{
			Stage: "node",
			Details: map[string]any{
				"nested": map[string]any{"value": "scheduler"},
			},
		},
	}}

	stepCalled := false
	stepConfig := fruntime.SchedulerConfig{
		StepObserver: func(_ context.Context, tasks []fruntime.GraphTask, observedState *state.State) error {
			stepCalled = true
			if observedState == currentState {
				t.Fatal("step observer received scheduler-owned state")
			}
			if err := observedState.SetSection(state.SectionShared, map[string]any{
				"nested": map[string]any{"value": "observer"},
			}); err != nil {
				t.Fatalf("mutate observed state: %v", err)
			}
			tasks[0].Failure.Details["nested"].(map[string]any)["value"] = "observer"
			tasks[0].Input = state.NewPatch(state.PatchOp{
				Kind:  state.OpSet,
				Path:  state.Shared("input"),
				Value: "observer",
			})
			return nil
		},
	}
	if err := runnable.notifyGraphStep(context.Background(), stepConfig, completedTasks, currentState); err != nil {
		t.Fatalf("notifyGraphStep() error = %v", err)
	}
	if !stepCalled {
		t.Fatal("step observer was not called")
	}
	stateValue, ok := state.NewAccess(currentState).ReadAny(state.Shared("nested"))
	if !ok || stateValue.(map[string]any)["value"] != "scheduler" {
		t.Fatalf("scheduler state = %#v, found = %v", stateValue, ok)
	}
	if value := completedTasks[0].Failure.Details["nested"].(map[string]any)["value"]; value != "scheduler" {
		t.Fatalf("scheduler failure details value = %#v", value)
	}
	operations := completedTasks[0].Input.Ops()
	if value := operations[0].Value.(map[string]any)["nested"].(map[string]any)["value"]; value != "scheduler" {
		t.Fatalf("scheduler task input value = %#v", value)
	}

	payload := map[string]any{
		"nested": map[string]any{
			"items": []any{map[string]any{"value": "scheduler"}},
		},
	}
	eventCalled := false
	eventConfig := fruntime.SchedulerConfig{
		EventObserver: func(_ context.Context, event fruntime.SchedulerEvent) error {
			eventCalled = true
			event.Payload["nested"].(map[string]any)["items"].([]any)[0].(map[string]any)["value"] = "observer"
			event.Payload["added"] = true
			return nil
		},
	}
	if err := runnable.notifySchedulerEvent(context.Background(), eventConfig, fruntime.SchedulerEvent{
		Type:    fruntime.SchedulerEventRouteDecision,
		NodeID:  "node-1",
		Payload: payload,
	}); err != nil {
		t.Fatalf("notifySchedulerEvent() error = %v", err)
	}
	if !eventCalled {
		t.Fatal("event observer was not called")
	}
	if _, exists := payload["added"]; exists {
		t.Fatal("event observer mutated scheduler-owned payload map")
	}
	if value := payload["nested"].(map[string]any)["items"].([]any)[0].(map[string]any)["value"]; value != "scheduler" {
		t.Fatalf("scheduler event payload value = %#v", value)
	}
}

func TestSchedulerObserversRejectOpaqueMutableValues(t *testing.T) {
	runnable := &scheduledRunnable{}
	opaque := &observerOpaqueMutable{values: []string{"scheduler"}}
	observerCalled := false
	stepConfig := fruntime.SchedulerConfig{
		StepObserver: func(context.Context, []fruntime.GraphTask, *state.State) error {
			observerCalled = true
			return nil
		},
	}

	err := runnable.notifyGraphStep(context.Background(), stepConfig, nil, state.FromShared(map[string]any{"opaque": opaque}))
	var stepErr *fruntime.GraphStepError
	if !errors.As(err, &stepErr) || !strings.Contains(err.Error(), "step observer state cannot be safely cloned") {
		t.Fatalf("opaque state error = %v", err)
	}
	if observerCalled {
		t.Fatal("step observer received opaque state")
	}

	err = runnable.notifyGraphStep(context.Background(), stepConfig, []fruntime.GraphTask{{
		TaskID: "opaque-input",
		NodeID: "node-1",
		Input: state.NewPatch(state.PatchOp{
			Kind:  state.OpSet,
			Path:  state.Shared("opaque"),
			Value: opaque,
		}),
	}}, state.NewState())
	if !errors.As(err, &stepErr) || !strings.Contains(err.Error(), "step observer tasks cannot be safely cloned") || !strings.Contains(err.Error(), "observerOpaqueMutable") {
		t.Fatalf("opaque task input error = %v", err)
	}
	if observerCalled {
		t.Fatal("step observer received opaque task input")
	}

	eventConfig := fruntime.SchedulerConfig{
		EventObserver: func(context.Context, fruntime.SchedulerEvent) error {
			observerCalled = true
			return nil
		},
	}
	err = runnable.notifySchedulerEvent(context.Background(), eventConfig, fruntime.SchedulerEvent{
		Type:    fruntime.SchedulerEventRouteDecision,
		Payload: map[string]any{"opaque": opaque},
	})
	if err == nil || !strings.Contains(err.Error(), "scheduler event \"route_decision\" payload cannot be safely cloned") {
		t.Fatalf("opaque event payload error = %v", err)
	}
	if observerCalled {
		t.Fatal("event observer received opaque payload")
	}
}
