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
	budget, ok := fruntime.LoadGraphExecutionBudget(restored.Business)
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
	budget, ok := fruntime.LoadGraphExecutionBudget(restored.Business)
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
	if len(steps) != 1 || steps[0].Status != fruntime.StepStatusFailed {
		t.Fatalf("steps = %#v", steps)
	}
}

func TestExecutionPolicyTimeoutRetryWaitsForPreviousAttempt(t *testing.T) {
	workflow := NewGraph(nil)
	started := make(chan int32, 2)
	releaseFirst := make(chan struct{})
	var attempts atomic.Int32
	mustAddNode(t, workflow, "slow", func(context.Context, *state.Access) error {
		attempt := attempts.Add(1)
		started <- attempt
		if attempt == 1 {
			<-releaseFirst
		}
		return nil
	})
	if err := workflow.SetEntryPoint("slow"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.AddEdge("slow", EndNodeRef); err != nil {
		t.Fatal(err)
	}
	policy := workflow.ExecutionPolicy()
	policy.Limits.MaxWallTime = time.Second
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
	case attempt := <-started:
		if attempt != 1 {
			t.Fatalf("first started attempt = %d", attempt)
		}
	case <-time.After(time.Second):
		t.Fatal("first attempt did not start")
	}
	time.Sleep(50 * time.Millisecond)
	select {
	case attempt := <-started:
		t.Fatalf("retry attempt %d overlapped the timed-out attempt", attempt)
	default:
	}
	close(releaseFirst)
	select {
	case attempt := <-started:
		if attempt != 2 {
			t.Fatalf("second started attempt = %d", attempt)
		}
	case <-time.After(time.Second):
		t.Fatal("retry did not start after the timed-out attempt exited")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run failed after retry: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("run did not complete after retry")
	}
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
