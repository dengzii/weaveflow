package runtime

import (
	"context"
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/llms"
)

func withRunnerEventContext(ctx context.Context, runner *GraphRunner, runID, stepID, nodeID string) core.Context {
	coreCtx := core.NewContext(ctx)
	if coreCtx.Model() == nil && len(coreCtx.Models()) == 0 && coreCtx.Tools() == nil {
		return coreCtx
	}
	ctx = core.WithModelCallObserver(ctx, modelCallEventObserver(runner, runID, stepID, nodeID))
	ctx = core.WithToolExecutionObserver(ctx, toolExecutionEventObserver(runner, runID, stepID, nodeID))
	ctx = withRunnerEventFailureReporter(ctx, runner.recordBestEffortEventFailure)
	return core.NewContext(ctx)
}

func modelCallEventObserver(runner *GraphRunner, runID, stepID, nodeID string) core.ModelCallObserver {
	return func(ctx context.Context, event core.ModelCallEvent) error {
		switch event.Stage {
		case core.ModelCallStream:
			eventType := EventLLMContentChunk
			if event.Stream.Type == llms.ModelStreamReasoning {
				eventType = EventLLMReasoningChunk
			}
			return PublishRunnerContextEvent(ctx, eventType, llmTextEventPayload(event.Request.CallID, event.Stream.Text))
		case core.ModelCallCompleted:
			publishModelResponseEvents(ctx, event.Request, event.Response)
		case core.ModelCallFailed:
			payload := map[string]any{"call_id": event.Request.CallID, "model": modelLabel(event.Request, event.Response), "calls": 1}
			if event.Response != nil {
				payload = buildModelCallPayload(event.Request, event.Response)
			}
			if event.Err != nil {
				payload["error"] = event.Err.Error()
				payload["error_class"] = core.ClassifyError(event.Err)
			}
			runner.publishBestEffortEvent(ctx, RunRecord{RunID: runID}, stepID, nodeID, EventLLMCall, payload)
		}
		return nil
	}
}

func publishModelResponseEvents(ctx context.Context, request llms.ModelRequest, response *llms.ModelResponse) {
	if response == nil {
		return
	}
	for _, choice := range response.Choices {
		if choice == nil {
			continue
		}
		if strings.TrimSpace(choice.ReasoningContent) != "" {
			publishRunnerContextEventBestEffort(ctx, EventLLMReasoning, llmTextEventPayload(request.CallID, choice.ReasoningContent))
		}
		if strings.TrimSpace(choice.Content) != "" {
			publishRunnerContextEventBestEffort(ctx, EventLLMContent, llmTextEventPayload(request.CallID, choice.Content))
		}
		for _, toolCall := range choice.ToolCalls {
			if toolCall.FunctionCall != nil {
				publishRunnerContextEventBestEffort(ctx, EventLLMFunctionCall, map[string]any{
					"call_id":      request.CallID,
					"tool_call_id": toolCall.ID,
					"name":         toolCall.FunctionCall.Name,
					"arguments":    toolCall.FunctionCall.Arguments,
				})
			}
		}
	}
	publishRunnerContextEventBestEffort(ctx, EventLLMCall, buildModelCallPayload(request, response))
}

func buildModelCallPayload(request llms.ModelRequest, response *llms.ModelResponse) map[string]any {
	usage := response.Usage.Normalized()
	payload := map[string]any{
		"call_id":              request.CallID,
		"model_id":             request.ModelID,
		"model":                modelLabel(request, response),
		"calls":                1,
		"prompt_tokens":        usage.InputTokens,
		"completion_tokens":    usage.OutputTokens,
		"total_tokens":         usage.TotalTokens,
		"reasoning_tokens":     usage.ReasoningTokens,
		"prompt_cached_tokens": usage.CachedInputTokens,
	}
	if len(response.Choices) > 0 && response.Choices[0] != nil {
		payload["stop_reason"] = strings.TrimSpace(response.Choices[0].StopReason)
	}
	if response.Cost != nil {
		payload["cost"] = response.Cost
		payload["cost_total"] = response.Cost.Total
		payload["cost_currency"] = response.Cost.Currency
	}
	return payload
}

func modelLabel(request llms.ModelRequest, response *llms.ModelResponse) string {
	if response != nil && strings.TrimSpace(response.Model) != "" {
		return strings.TrimSpace(response.Model)
	}
	if strings.TrimSpace(request.Model) != "" {
		return strings.TrimSpace(request.Model)
	}
	return strings.TrimSpace(request.ModelID)
}

func llmTextEventPayload(callID, text string) map[string]any {
	return map[string]any{"call_id": callID, "text": text}
}

func toolExecutionEventObserver(runner *GraphRunner, runID, stepID, nodeID string) core.ToolExecutionObserver {
	return func(ctx context.Context, event core.ToolExecutionEvent) {
		payload := toolExecutionEventPayload(event)
		switch event.Stage {
		case core.ToolExecutionRequested:
			runner.publishBestEffortEvent(ctx, RunRecord{RunID: runID}, stepID, nodeID, EventToolCalled, payload)
			_, _ = SaveJSONArtifactBestEffort(ctx, "tool.input", payload)
		case core.ToolExecutionApprovalNeeded:
			runner.publishBestEffortEvent(ctx, RunRecord{RunID: runID}, stepID, nodeID, EventToolApprovalNeeded, payload)
		case core.ToolExecutionApproved:
			runner.publishBestEffortEvent(ctx, RunRecord{RunID: runID}, stepID, nodeID, EventToolApproved, payload)
		case core.ToolExecutionStarted:
			runner.publishBestEffortEvent(ctx, RunRecord{RunID: runID}, stepID, nodeID, EventToolStarted, payload)
		case core.ToolExecutionDenied:
			runner.publishBestEffortEvent(ctx, RunRecord{RunID: runID}, stepID, nodeID, EventToolDenied, payload)
			runner.publishBestEffortEvent(ctx, RunRecord{RunID: runID}, stepID, nodeID, EventToolFailed, payload)
			_, _ = SaveJSONArtifactBestEffort(ctx, "tool.output", payload)
		case core.ToolExecutionFailed:
			runner.publishBestEffortEvent(ctx, RunRecord{RunID: runID}, stepID, nodeID, EventToolFailed, payload)
			_, _ = SaveJSONArtifactBestEffort(ctx, "tool.output", payload)
		case core.ToolExecutionReturned:
			runner.publishBestEffortEvent(ctx, RunRecord{RunID: runID}, stepID, nodeID, EventToolReturned, payload)
			_, _ = SaveJSONArtifactBestEffort(ctx, "tool.output", payload)
		}
	}
}

func toolExecutionEventPayload(event core.ToolExecutionEvent) map[string]any {
	payload := map[string]any{
		"tool_call_id":  event.Call.ID,
		"name":          event.Tool.Name(),
		"permissions":   event.Tool.Permissions,
		"approval_mode": event.Tool.Approval,
	}
	if event.Call.FunctionCall != nil {
		payload["arguments"] = event.Call.FunctionCall.Arguments
	}
	if event.Result.Content != "" {
		payload["content"] = event.Result.Content
	}
	if event.Result.Value != nil {
		payload["value"] = event.Result.Value
	}
	if event.Result.IsError {
		payload["is_error"] = true
		payload["error_code"] = event.Result.ErrorCode
		payload["error"] = event.Result.ErrorMessage
	}
	if event.Err != nil {
		payload["error"] = event.Err.Error()
		payload["error_class"] = core.ClassifyError(event.Err)
	}
	if event.Approval != nil {
		payload["approval"] = event.Approval
	}
	if !event.StartedAt.IsZero() && !event.FinishedAt.IsZero() {
		payload["duration_ms"] = max(event.FinishedAt.Sub(event.StartedAt).Milliseconds(), 0)
	}
	return payload
}
