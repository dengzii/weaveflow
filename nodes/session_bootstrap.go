package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"weaveflow/dsl"
	fruntime "weaveflow/runtime"

	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
)

const defaultSessionBootstrapInputPath = fruntime.StateKeyRequest + ".input"

type SessionBootstrapNode struct {
	NodeInfo
	StateScope      string
	Input           string
	InputPath       string
	SystemPrompt    string
	MaxIterations   int
	AgentProfile    map[string]any
	RequestMetadata map[string]any
	ToolPolicy      map[string]any
}

func NewSessionBootstrapNode() *SessionBootstrapNode {
	id := uuid.New()
	return &SessionBootstrapNode{
		NodeInfo: NodeInfo{
			NodeID:          "SessionBootstrap_" + id.String(),
			NodeName:        "SessionBootstrap",
			NodeDescription: "Initialize request, agent, tool policy, and scoped conversation state for an agent run.",
		},
		MaxIterations: fruntime.DefaultMaxIterations,
	}
}

func (n *SessionBootstrapNode) Invoke(ctx context.Context, state fruntime.State) (fruntime.State, error) {
	if state == nil {
		state = fruntime.State{}
	}

	input, err := n.resolveInput(state)
	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "session.bootstrap.error", map[string]any{"error": err.Error()})
		return state, err
	}

	request := state.Ensure(fruntime.StateKeyRequest)
	if request == nil {
		return state, errors.New("session bootstrap request state is unavailable")
	}
	request["input"] = input
	mergeBootstrapMap(request, "metadata", n.RequestMetadata)

	agent := state.Ensure(fruntime.StateKeyAgent)
	if agent == nil {
		return state, errors.New("session bootstrap agent state is unavailable")
	}
	mergeBootstrapMap(agent, "profile", n.AgentProfile)

	toolPolicy := state.Ensure(fruntime.StateKeyToolPolicy)
	if toolPolicy == nil {
		return state, errors.New("session bootstrap tool policy state is unavailable")
	}
	mergeBootstrapValues(toolPolicy, n.ToolPolicy)

	conversation := fruntime.Conversation(state, n.StateScope)
	messages := conversation.Messages()
	if len(messages) == 0 {
		messages = n.initialMessages(input)
		if len(messages) > 0 {
			conversation.UpdateMessage(messages)
		}
	}
	conversation.SetMaxIterations(n.effectiveMaxIterations())

	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, map[string]any{
		"kind":           "session_bootstrap",
		"state_scope":    strings.TrimSpace(n.StateScope),
		"has_input":      strings.TrimSpace(input) != "",
		"max_iterations": n.effectiveMaxIterations(),
	})
	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "session.bootstrap", n.artifactPayload(state, input))

	return state, nil
}

func (n *SessionBootstrapNode) GraphNodeSpec() dsl.GraphNodeSpec {
	config := map[string]any{
		"state_scope":    n.StateScope,
		"max_iterations": n.effectiveMaxIterations(),
	}
	if input := strings.TrimSpace(n.Input); input != "" {
		config["input"] = input
	}
	if inputPath := strings.TrimSpace(n.InputPath); inputPath != "" {
		config["input_path"] = inputPath
	}
	if systemPrompt := strings.TrimSpace(n.SystemPrompt); systemPrompt != "" {
		config["system_prompt"] = systemPrompt
	}
	if len(n.AgentProfile) > 0 {
		config["agent_profile"] = cloneBootstrapMap(n.AgentProfile)
	}
	if len(n.RequestMetadata) > 0 {
		config["request_metadata"] = cloneBootstrapMap(n.RequestMetadata)
	}
	if len(n.ToolPolicy) > 0 {
		config["tool_policy"] = cloneBootstrapMap(n.ToolPolicy)
	}

	return dsl.GraphNodeSpec{
		ID:          n.ID(),
		Name:        n.Name(),
		Type:        "session_bootstrap",
		Description: n.Description(),
		Config:      config,
	}
}

func (n *SessionBootstrapNode) resolveInput(state fruntime.State) (string, error) {
	if input := strings.TrimSpace(n.Input); input != "" {
		return input, nil
	}

	if inputPath := strings.TrimSpace(n.InputPath); inputPath != "" {
		value, ok := fruntime.ResolveStatePath(state, inputPath)
		if !ok {
			return "", fmt.Errorf("session bootstrap input not found at %q", inputPath)
		}
		return strings.TrimSpace(stringifyBootstrapValue(value)), nil
	}

	if value, ok := fruntime.ResolveStatePath(state, defaultSessionBootstrapInputPath); ok {
		return strings.TrimSpace(stringifyBootstrapValue(value)), nil
	}

	messages := fruntime.Conversation(state, n.StateScope).Messages()
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != llms.ChatMessageTypeHuman {
			continue
		}
		if text := strings.TrimSpace(extractText(messages[i])); text != "" {
			return text, nil
		}
	}

	return "", nil
}

func (n *SessionBootstrapNode) initialMessages(input string) []llms.MessageContent {
	messages := make([]llms.MessageContent, 0, 2)
	if systemPrompt := strings.TrimSpace(n.SystemPrompt); systemPrompt != "" {
		messages = append(messages, llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt))
	}
	if input = strings.TrimSpace(input); input != "" {
		messages = append(messages, llms.TextParts(llms.ChatMessageTypeHuman, input))
	}
	return messages
}

func (n *SessionBootstrapNode) effectiveMaxIterations() int {
	if n == nil || n.MaxIterations <= 0 {
		return fruntime.DefaultMaxIterations
	}
	return n.MaxIterations
}

func (n *SessionBootstrapNode) artifactPayload(state fruntime.State, input string) map[string]any {
	payload := map[string]any{
		"state_scope":    strings.TrimSpace(n.StateScope),
		"input":          input,
		"max_iterations": n.effectiveMaxIterations(),
		"request":        cloneBootstrapMap(state.Get(fruntime.StateKeyRequest)),
		"agent":          cloneBootstrapMap(state.Get(fruntime.StateKeyAgent)),
		"tool_policy":    cloneBootstrapMap(state.Get(fruntime.StateKeyToolPolicy)),
	}
	if messages, err := fruntime.SerializeMessages(fruntime.Conversation(state, n.StateScope).Messages()); err == nil {
		payload["messages"] = messages
	}
	return payload
}

func mergeBootstrapMap(target fruntime.State, key string, values map[string]any) {
	if target == nil || key == "" {
		return
	}

	existing, _ := target[key].(map[string]any)
	if existing == nil {
		if typed, ok := target[key].(fruntime.State); ok {
			existing = typed
		}
	}
	if existing == nil {
		existing = map[string]any{}
	}
	mergeBootstrapValues(existing, values)
	target[key] = existing
}

func mergeBootstrapValues(target map[string]any, values map[string]any) {
	if target == nil {
		return
	}
	for key, value := range values {
		target[key] = cloneBootstrapValue(value)
	}
}

func stringifyBootstrapValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(value)
		}
		return string(data)
	}
}

func cloneBootstrapMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = cloneBootstrapValue(value)
	}
	return cloned
}

func cloneBootstrapValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneBootstrapMap(typed)
	case fruntime.State:
		return fruntime.State(cloneBootstrapMap(typed))
	case []any:
		cloned := make([]any, len(typed))
		for i, item := range typed {
			cloned[i] = cloneBootstrapValue(item)
		}
		return cloned
	case []string:
		cloned := make([]string, len(typed))
		copy(cloned, typed)
		return cloned
	default:
		return value
	}
}
