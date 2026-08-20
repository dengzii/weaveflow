package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/core"
)

type AgentInvocationKind string

const (
	AgentInvocationModel AgentInvocationKind = "model"
	AgentInvocationTool  AgentInvocationKind = "tool"
)

type AgentInvocationStatus string

const (
	AgentInvocationStarted   AgentInvocationStatus = "started"
	AgentInvocationSucceeded AgentInvocationStatus = "succeeded"
	AgentInvocationFailed    AgentInvocationStatus = "failed"
)

type AgentInvocation struct {
	ID           string                `json:"invocation_id"`
	Kind         AgentInvocationKind   `json:"kind"`
	OperationID  string                `json:"operation_id,omitempty"`
	Iteration    int                   `json:"iteration"`
	ToolCallID   string                `json:"tool_call_id,omitempty"`
	ToolName     string                `json:"tool_name,omitempty"`
	StepID       string                `json:"step_id,omitempty"`
	CheckpointID string                `json:"checkpoint_id,omitempty"`
	Status       AgentInvocationStatus `json:"status"`
}

type agentInvocationKey struct{}

func NewAgentInvocationID(ctx context.Context, kind AgentInvocationKind, iteration int, toolCallID, toolName string) string {
	metadata, _ := RunnerMetadataFromContext(ctx)
	operation, _ := operationIdentity(ctx)
	return stableRuntimeID("agent-invocation", metadata.RunID, metadata.StepID, metadata.TaskID, metadata.NodeID, fmt.Sprint(metadata.Attempt), string(kind), fmt.Sprint(iteration), toolCallID, toolName, operation)
}

func WithAgentInvocation(ctx context.Context, invocation AgentInvocation) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(invocation.ID) == "" {
		invocation.ID = NewAgentInvocationID(ctx, invocation.Kind, invocation.Iteration, invocation.ToolCallID, invocation.ToolName)
	}
	return context.WithValue(ctx, agentInvocationKey{}, invocation)
}

func AgentInvocationFromContext(ctx context.Context) (AgentInvocation, bool) {
	if ctx == nil {
		return AgentInvocation{}, false
	}
	invocation, ok := ctx.Value(agentInvocationKey{}).(AgentInvocation)
	return invocation, ok && strings.TrimSpace(invocation.ID) != ""
}

func BeginAgentInvocation(ctx context.Context, invocation AgentInvocation) (context.Context, func(error) error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(invocation.ID) == "" {
		invocation.ID = NewAgentInvocationID(ctx, invocation.Kind, invocation.Iteration, invocation.ToolCallID, invocation.ToolName)
	}
	invocation.Status = AgentInvocationStarted
	recorder, _ := AgentInvocationRecorderFromContext(ctx)
	var startErr error
	if recorder != nil {
		invocation, startErr = recorder.Start(ctx, invocation)
	}
	invocationCtx := WithAgentInvocation(ctx, invocation)
	publishAgentInvocationEvent(invocationCtx, "agent.invocation.started", invocation, "")
	return invocationCtx, func(invocationErr error) error {
		if startErr != nil && invocationErr == nil {
			invocationErr = startErr
		}
		finished := invocation
		finished.Status = AgentInvocationSucceeded
		if invocationErr != nil {
			finished.Status = AgentInvocationFailed
		}
		message := ""
		if invocationErr != nil {
			message = invocationErr.Error()
		}
		publishAgentInvocationEvent(invocationCtx, "agent.invocation.finished", finished, message)
		if recorder != nil {
			return errors.Join(invocationErr, recorder.Finish(invocationCtx, finished, invocationErr))
		}
		return invocationErr
	}
}

func CheckpointAgentInvocation(ctx context.Context, stage string) error {
	invocation, ok := AgentInvocationFromContext(ctx)
	if !ok {
		return nil
	}
	recorder, _ := AgentInvocationRecorderFromContext(ctx)
	if recorder != nil {
		var err error
		invocation, err = recorder.Checkpoint(ctx, invocation, strings.TrimSpace(stage))
		if err != nil {
			return err
		}
		ctx = WithAgentInvocation(ctx, invocation)
	}
	publishAgentInvocationEvent(ctx, "agent.invocation.checkpoint", invocation, strings.TrimSpace(stage))
	return nil
}

func publishAgentInvocationEvent(ctx context.Context, eventName string, invocation AgentInvocation, detail string) {
	payload := map[string]any{
		"event":            eventName,
		"agent_invocation": invocation,
	}
	if detail != "" {
		payload["detail"] = detail
	}
	_ = PublishRunnerContextEvent(ctx, EventNodeCustom, payload)
}

func operationIdentity(ctx context.Context) (string, bool) {
	if operation, ok := core.EffectOperationFromContext(ctx); ok && operation.Key != "" {
		return operation.Key, true
	}
	return "", false
}
