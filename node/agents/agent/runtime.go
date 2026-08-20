package agent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/llms/parts"
	basenode "github.com/dengzii/weaveflow/node"
	executor "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"

	"github.com/dengzii/weaveflow/llms"
)

type executionIdentity struct {
	NodeID           string
	ToolName         string
	ConversationPath string
}

type loopRunner struct {
	config                  Config
	identity                executionIdentity
	strictToolIDs           bool
	responseName            string
	outputSchema            state.JSONSchema
	outputJSON              bool
	outputJSONCompatibility bool
	resumePhase             string
}

func newNodeRuntime(target *Node) loopRunner {
	if target == nil {
		return loopRunner{}
	}
	return loopRunner{
		config: target.Config,
		identity: executionIdentity{
			NodeID:           target.ID(),
			ConversationPath: target.ConversationPath.String(),
		},
		responseName:            target.ResponseName,
		outputSchema:            target.OutputSchema.Clone(),
		outputJSON:              target.outputJSONEnabled(),
		outputJSONCompatibility: target.OutputJSONCompatibility,
	}
}

func (runtime loopRunner) runLoop(ctx core.Context, conversation *conversationcap.View) error {
	model := ctx.Model(runtime.config.ModelID)
	if model == nil {
		return fmt.Errorf("agent: model %q not available", effectiveModelID(runtime.config.ModelID))
	}

	selectedTools, err := runtime.selectTools(ctx.Tools())
	if err != nil {
		return err
	}
	ctx = core.NewContext(core.WithTools(ctx, selectedTools))

	toolSets := make([]llms.ToolDefinition, 0, len(selectedTools))
	toolIDs := make([]string, 0, len(selectedTools))
	for toolID := range selectedTools {
		toolIDs = append(toolIDs, toolID)
	}
	sort.Strings(toolIDs)
	for _, toolID := range toolIDs {
		toolSets = append(toolSets, selectedTools[toolID].Definition())
	}

	maxIterations := runtime.effectiveMaxIterations(conversation)
	promptMaxChars := runtime.effectivePromptMaxChars()

	for {
		if conversation.IterationCount() >= maxIterations {
			limitErr := core.NewExecutionError(core.ErrorResourceExhausted, "agent iteration budget exceeded", nil, map[string]any{
				"kind": "iterations", "limit": maxIterations, "actual": conversation.IterationCount(),
			})
			runtime.publishLoopStopped(ctx, conversation.IterationCount(), "max_iterations")
			runtime.saveErrorArtifact(ctx, conversation.IterationCount(), limitErr)
			return limitErr
		}
		if runtime.resumePhase == executor.AgentResumePhaseToolCalls {
			runtime.resumePhase = ""
			pendingCalls := pendingToolCalls(conversation)
			if len(pendingCalls) > 0 {
				if err := runtime.executeToolCalls(ctx, conversation, pendingCalls, max(conversation.IterationCount()-1, 0)); err != nil {
					return err
				}
				continue
			}
		}

		messages := conversation.Messages()
		promptMessages := basenode.TrimLLMPromptMessages(messages, promptMaxChars)
		iteration := conversation.IterationCount()
		if payload, err := buildPromptArtifact(runtime.identity, promptMessages, toolSets, iteration, maxIterations, runtime.responseName, runtime.outputSchema, runtime.outputJSON, runtime.outputJSONCompatibility); err == nil {
			_, _ = executor.SaveJSONArtifactBestEffort(ctx, "agent.llm.prompt", payload)
		}

		modelInvocation := executor.AgentInvocation{
			Kind: executor.AgentInvocationModel, Iteration: iteration,
			OperationID: runtime.identity.NodeID + ":model:" + fmt.Sprint(iteration),
		}
		modelCtx, finishModelInvocation := executor.BeginAgentInvocation(ctx, modelInvocation)
		modelInvocationRecord, _ := executor.AgentInvocationFromContext(modelCtx)
		if _, budgetErr := executor.ConsumeAgentBudget(modelCtx, runtime.budgetLimits(maxIterations), executor.AgentBudgetUsage{Iterations: 1}); budgetErr != nil {
			if finishErr := finishModelInvocation(budgetErr); finishErr != nil {
				budgetErr = errors.Join(budgetErr, finishErr)
			}
			runtime.saveErrorArtifact(ctx, iteration, budgetErr)
			return budgetErr
		}
		response, err := core.GenerateModel(modelCtx, model, llms.ModelRequest{
			CallID:                    modelInvocationRecord.ID,
			ModelID:                   effectiveModelID(runtime.config.ModelID),
			Mode:                      llms.ModelModeChat,
			Messages:                  promptMessages,
			Tools:                     toolSets,
			Thinking:                  llms.ThinkingModeHigh,
			ResponseName:              strings.TrimSpace(runtime.responseName),
			ResponseSchema:            runtime.outputSchema.Clone(),
			StrictResponse:            len(runtime.outputSchema) > 0,
			ResponseJSON:              runtime.outputJSON,
			ResponseJSONCompatibility: runtime.outputJSONCompatibility,
		})
		if err != nil {
			if finishErr := finishModelInvocation(err); finishErr != nil {
				err = errors.Join(err, finishErr)
			}
			runtime.saveErrorArtifact(ctx, iteration, err)
			return err
		}
		if response == nil || len(response.Choices) == 0 || response.Choices[0] == nil {
			err := errors.New("agent: llm returned no choices")
			if finishErr := finishModelInvocation(err); finishErr != nil {
				err = errors.Join(err, finishErr)
			}
			runtime.saveErrorArtifact(ctx, iteration, err)
			return err
		}
		usage := response.Usage.Normalized()
		cost := 0.0
		if response.Cost != nil {
			cost = response.Cost.Total
		}
		if _, budgetErr := executor.ConsumeAgentBudget(modelCtx, runtime.budgetLimits(maxIterations), executor.AgentBudgetUsage{
			TotalTokens: usage.TotalTokens,
			Cost:        cost,
		}); budgetErr != nil {
			if finishErr := finishModelInvocation(budgetErr); finishErr != nil {
				budgetErr = errors.Join(budgetErr, finishErr)
			}
			runtime.saveErrorArtifact(ctx, iteration, budgetErr)
			return budgetErr
		}
		if finishErr := finishModelInvocation(nil); finishErr != nil {
			runtime.saveErrorArtifact(ctx, iteration, finishErr)
			return finishErr
		}
		if payload := buildResponseArtifact(runtime.identity, response, iteration, runtime.responseName, runtime.outputSchema, runtime.outputJSON, runtime.outputJSONCompatibility); len(payload.Choices) > 0 {
			_, _ = executor.SaveJSONArtifactBestEffort(ctx, "agent.llm.response", payload)
		}

		choice := response.Choices[0]
		aiMessage := llms.MessageContent{Role: llms.ChatMessageTypeAI}
		if strings.TrimSpace(choice.ReasoningContent) != "" {
			aiMessage.Parts = append(aiMessage.Parts, parts.NewReasoningPart(choice.ReasoningContent))
		}
		if strings.TrimSpace(choice.Content) != "" {
			aiMessage.Parts = append(aiMessage.Parts, llms.TextPart(choice.Content))
		}
		for _, toolCall := range choice.ToolCalls {
			if toolCall.Type == "" {
				payload := runtime.identityPayload(map[string]any{
					"message":         "agent received a tool call with no type",
					"agent_iteration": iteration,
				})
				_ = executor.PublishRunnerContextEvent(ctx, executor.EventWarning, payload)
				continue
			}
			aiMessage.Parts = append(aiMessage.Parts, toolCall)
		}

		if err := conversation.SetMessages(append(messages, aiMessage)); err != nil {
			return err
		}
		if err := conversation.IncrementIteration(); err != nil {
			return err
		}
		if checkpointErr := executor.CheckpointAgentInvocation(modelCtx, executor.AgentResumePhaseToolCalls); checkpointErr != nil {
			return checkpointErr
		}

		if len(choice.ToolCalls) == 0 {
			answer, _, err := normalizeFinalOutput(basenode.ExtractText(aiMessage), runtime.outputSchema, runtime.outputJSON, runtime.outputJSONCompatibility)
			if err != nil {
				return err
			}
			if err := conversation.SetFinalAnswer(answer); err != nil {
				return err
			}
			runtime.publishLoopStopped(ctx, conversation.IterationCount(), "final_answer")
			return nil
		}

		if err := runtime.executeToolCalls(ctx, conversation, choice.ToolCalls, iteration); err != nil {
			return err
		}
	}
}

func pendingToolCalls(conversation *conversationcap.View) []llms.ToolCall {
	if conversation == nil {
		return nil
	}
	messages := conversation.Messages()
	if len(messages) == 0 {
		return nil
	}
	last := messages[len(messages)-1]
	if last.Role != llms.ChatMessageTypeAI {
		return nil
	}
	completed := make(map[string]struct{})
	for _, message := range messages {
		if message.Role != llms.ChatMessageTypeTool {
			continue
		}
		for _, part := range message.Parts {
			result, ok := part.(llms.ToolResult)
			if ok && strings.TrimSpace(result.ToolCallID) != "" {
				completed[result.ToolCallID] = struct{}{}
			}
		}
	}
	calls := make([]llms.ToolCall, 0)
	for _, part := range last.Parts {
		call, ok := part.(llms.ToolCall)
		if ok && (strings.TrimSpace(call.ID) == "" || func() bool {
			_, found := completed[call.ID]
			return !found
		}()) {
			calls = append(calls, call)
		}
	}
	return calls
}

func normalizeFinalOutput(content string, schema state.JSONSchema, outputJSON, compatibility bool) (string, any, error) {
	content = strings.TrimSpace(content)
	if !outputJSON {
		return content, content, nil
	}
	return core.DecodeStructuredOutput(content, schema, compatibility)
}

func (runtime loopRunner) selectTools(available map[string]core.Tool) (map[string]core.Tool, error) {
	if len(runtime.config.ToolIDs) == 0 {
		return nil, nil
	}
	selected := make(map[string]core.Tool, len(runtime.config.ToolIDs))
	for _, configuredID := range runtime.config.ToolIDs {
		toolID := strings.TrimSpace(configuredID)
		tool, ok := available[toolID]
		if !ok {
			if runtime.strictToolIDs {
				return nil, fmt.Errorf("agent tool %q: configured tool %q is not available", runtime.identity.ToolName, toolID)
			}
			continue
		}
		if runtime.identity.ToolName != "" && (strings.EqualFold(toolID, runtime.identity.ToolName) || strings.EqualFold(tool.Name(), runtime.identity.ToolName)) {
			return nil, fmt.Errorf("agent tool %q cannot call itself", runtime.identity.ToolName)
		}
		selected[toolID] = tool
	}
	if len(selected) == 0 {
		return nil, nil
	}
	return selected, nil
}

func (runtime loopRunner) executeToolCalls(ctx core.Context, conversation *conversationcap.View, toolCalls []llms.ToolCall, iteration int) error {
	if len(toolCalls) == 0 {
		return nil
	}
	if _, err := executor.ConsumeAgentBudget(ctx, runtime.budgetLimits(runtime.effectiveMaxIterations(conversation)), executor.AgentBudgetUsage{ToolCalls: len(toolCalls)}); err != nil {
		return err
	}

	type toolExecution struct {
		message       llms.MessageContent
		invocationCtx context.Context
		finish        func(error) error
		toolErr       error
	}
	toolExecutions := make([]toolExecution, len(toolCalls))
	var executionErrors []error
	if runtime.config.Parallel && len(toolCalls) > 1 {
		type toolTask struct {
			index int
			call  llms.ToolCall
		}
		workerCount := len(toolCalls)
		if limit := core.ToolExecutionConcurrencyLimit(ctx); limit > 0 {
			workerCount = min(workerCount, limit)
		}
		tasks := make(chan toolTask)
		var waitGroup sync.WaitGroup
		waitGroup.Add(workerCount)
		for range workerCount {
			go func() {
				defer waitGroup.Done()
				for task := range tasks {
					toolExecutions[task.index] = runtime.executeToolCall(ctx, task.call, iteration, task.index)
				}
			}()
		}
		for index, toolCall := range toolCalls {
			tasks <- toolTask{index: index, call: toolCall}
		}
		close(tasks)
		waitGroup.Wait()
	} else {
		for index, toolCall := range toolCalls {
			toolExecutions[index] = runtime.executeToolCall(ctx, toolCall, iteration, index)
		}
	}

	for _, execution := range toolExecutions {
		if err := conversation.SetMessages(append(conversation.Messages(), execution.message)); err != nil {
			executionErrors = append(executionErrors, err)
			continue
		}
		if err := executor.CheckpointAgentInvocation(execution.invocationCtx, "tool_result"); err != nil {
			executionErrors = append(executionErrors, err)
		}
		if err := execution.finish(execution.toolErr); err != nil {
			executionErrors = append(executionErrors, err)
		}
	}
	return errors.Join(executionErrors...)
}

func (runtime loopRunner) budgetLimits(maxIterations int) executor.AgentBudgetLimits {
	return executor.AgentBudgetLimits{
		MaxIterations: maxIterations,
		MaxToolCalls:  runtime.config.MaxToolCalls,
		MaxTokens:     runtime.config.MaxTokens,
		MaxCost:       runtime.config.MaxCost,
	}
}

func (runtime loopRunner) executeToolCall(ctx core.Context, toolCall llms.ToolCall, iteration, index int) (toolExecution struct {
	message       llms.MessageContent
	invocationCtx context.Context
	finish        func(error) error
	toolErr       error
}) {
	toolName := ""
	if toolCall.FunctionCall != nil {
		toolName = toolCall.FunctionCall.Name
	}
	invocation := executor.AgentInvocation{
		Kind: executor.AgentInvocationTool, Iteration: iteration, ToolCallID: toolCall.ID, ToolName: toolName,
		OperationID: runtime.identity.NodeID + ":tool:" + fmt.Sprint(iteration) + ":" + fmt.Sprint(index),
	}
	toolCtx, finish := executor.BeginAgentInvocation(ctx, invocation)
	result, toolErr := basenode.ExecuteToolCall(core.NewContext(toolCtx), toolCall)
	if toolErr != nil {
		result.ToolCallID = toolCall.ID
		result.Name = toolName
		result.IsError = true
		result.ErrorCode = string(core.ClassifyError(toolErr))
		result.ErrorMessage = toolErr.Error()
	}
	return struct {
		message       llms.MessageContent
		invocationCtx context.Context
		finish        func(error) error
		toolErr       error
	}{
		message: llms.MessageContent{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{result}}, invocationCtx: toolCtx,
		finish: finish, toolErr: toolErr,
	}
}

func (runtime loopRunner) seedConversation(conversation *conversationcap.View, task string) error {
	messages := conversation.Messages()
	hasSystem := false
	hasAnyHuman := false
	for _, message := range messages {
		switch message.Role {
		case llms.ChatMessageTypeSystem:
			hasSystem = true
		case llms.ChatMessageTypeHuman:
			hasAnyHuman = true
		}
	}

	updated := false
	if strings.TrimSpace(runtime.config.SystemPrompt) != "" && !hasSystem {
		messages = append([]llms.MessageContent{llms.TextParts(llms.ChatMessageTypeSystem, runtime.config.SystemPrompt)}, messages...)
		updated = true
	}
	if task != "" && !hasAnyHuman {
		messages = append(messages, llms.TextParts(llms.ChatMessageTypeHuman, task))
		updated = true
	}
	if updated {
		return conversation.SetMessages(messages)
	}
	return nil
}

func (runtime loopRunner) effectiveMaxIterations(conversation *conversationcap.View) int {
	if runtime.config.MaxIterations > 0 {
		return runtime.config.MaxIterations
	}
	if conversation != nil {
		if maxIterations := conversation.MaxIterations(); maxIterations > 0 {
			return maxIterations
		}
	}
	return conversationcap.DefaultMaxIterations
}

func (runtime loopRunner) effectivePromptMaxChars() int {
	if runtime.config.PromptMaxChars <= 0 {
		return defaultPromptMaxChars
	}
	return runtime.config.PromptMaxChars
}

func (runtime loopRunner) publishLoopStopped(ctx context.Context, iteration int, reason string) {
	payload := runtime.identityPayload(map[string]any{
		"agent_iteration": iteration,
		"reason":          reason,
		"event":           "agent.loop_stopped",
	})
	_ = executor.PublishRunnerContextEvent(ctx, executor.EventNodeCustom, payload)
}

func (runtime loopRunner) saveErrorArtifact(ctx context.Context, iteration int, agentErr error) {
	payload := runtime.identityPayload(map[string]any{
		"agent_iteration": iteration,
		"error":           agentErr.Error(),
	})
	_, _ = executor.SaveJSONArtifactBestEffort(ctx, "agent.llm.error", payload)
}

func (runtime loopRunner) identityPayload(payload map[string]any) map[string]any {
	if runtime.identity.NodeID != "" {
		payload["agent_node_id"] = runtime.identity.NodeID
	}
	if runtime.identity.ToolName != "" {
		payload["agent_tool_name"] = runtime.identity.ToolName
	}
	return payload
}

func effectiveModelID(modelID string) string {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return core.DefaultModelID
	}
	return modelID
}
