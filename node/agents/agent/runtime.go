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
	fruntime "github.com/dengzii/weaveflow/runtime"

	"github.com/dengzii/weaveflow/llms"
)

type executionIdentity struct {
	NodeID           string
	ToolName         string
	ConversationPath string
}

type agentRuntime struct {
	config        Config
	identity      executionIdentity
	strictToolIDs bool
}

func newNodeRuntime(target *Node) agentRuntime {
	if target == nil {
		return agentRuntime{}
	}
	return agentRuntime{
		config: target.Config,
		identity: executionIdentity{
			NodeID:           target.ID(),
			ConversationPath: target.ConversationPath.String(),
		},
	}
}

func (runtime agentRuntime) runLoop(ctx core.Context, conversation *conversationcap.View) error {
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
			message := "Maximum agent iterations reached. The agent stopped before producing a final answer."
			if err := conversation.SetMessages(append(conversation.Messages(), llms.TextParts(llms.ChatMessageTypeAI, message))); err != nil {
				return err
			}
			if err := conversation.SetFinalAnswer(message); err != nil {
				return err
			}
			runtime.publishLoopStopped(ctx, conversation.IterationCount(), "max_iterations")
			return nil
		}

		messages := conversation.Messages()
		promptMessages := basenode.TrimLLMPromptMessages(messages, promptMaxChars)
		iteration := conversation.IterationCount()
		if payload, err := buildPromptArtifact(runtime.identity, promptMessages, toolSets, iteration, maxIterations); err == nil {
			_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "agent.llm.prompt", payload)
		}

		response, err := core.GenerateModel(ctx, model, llms.ModelRequest{
			ModelID:  effectiveModelID(runtime.config.ModelID),
			Mode:     llms.ModelModeChat,
			Messages: promptMessages,
			Tools:    toolSets,
			Thinking: llms.ThinkingModeHigh,
		})
		if err != nil {
			runtime.saveErrorArtifact(ctx, iteration, err)
			return err
		}
		if response == nil || len(response.Choices) == 0 || response.Choices[0] == nil {
			err := errors.New("agent: llm returned no choices")
			runtime.saveErrorArtifact(ctx, iteration, err)
			return err
		}
		if payload := buildResponseArtifact(runtime.identity, response, iteration); len(payload.Choices) > 0 {
			_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "agent.llm.response", payload)
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
				_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventWarning, payload)
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

		if len(choice.ToolCalls) == 0 {
			if err := conversation.SetFinalAnswer(basenode.ExtractText(aiMessage)); err != nil {
				return err
			}
			runtime.publishLoopStopped(ctx, conversation.IterationCount(), "final_answer")
			return nil
		}

		if err := runtime.executeToolCalls(ctx, conversation, choice.ToolCalls); err != nil {
			return err
		}
	}
}

func (runtime agentRuntime) selectTools(available map[string]core.Tool) (map[string]core.Tool, error) {
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

func (runtime agentRuntime) executeToolCalls(ctx core.Context, conversation *conversationcap.View, toolCalls []llms.ToolCall) error {
	if len(toolCalls) == 0 {
		return nil
	}

	toolMessages := make([]llms.MessageContent, len(toolCalls))
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
					toolMessages[task.index] = basenode.ExecuteToolCallMessage(ctx, task.call)
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
			toolMessages[index] = basenode.ExecuteToolCallMessage(ctx, toolCall)
		}
	}

	return conversation.SetMessages(append(conversation.Messages(), toolMessages...))
}

func (runtime agentRuntime) seedConversation(conversation *conversationcap.View, task string) error {
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

func (runtime agentRuntime) effectiveMaxIterations(conversation *conversationcap.View) int {
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

func (runtime agentRuntime) effectivePromptMaxChars() int {
	if runtime.config.PromptMaxChars <= 0 {
		return defaultPromptMaxChars
	}
	return runtime.config.PromptMaxChars
}

func (runtime agentRuntime) publishLoopStopped(ctx context.Context, iteration int, reason string) {
	payload := runtime.identityPayload(map[string]any{
		"agent_iteration": iteration,
		"reason":          reason,
		"event":           "agent.loop_stopped",
	})
	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, payload)
}

func (runtime agentRuntime) saveErrorArtifact(ctx context.Context, iteration int, agentErr error) {
	payload := runtime.identityPayload(map[string]any{
		"agent_iteration": iteration,
		"error":           agentErr.Error(),
	})
	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "agent.llm.error", payload)
}

func (runtime agentRuntime) identityPayload(payload map[string]any) map[string]any {
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
