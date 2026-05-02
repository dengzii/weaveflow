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
	"go.uber.org/zap"
)

const (
	defaultOrchestrationStatePath = fruntime.StateKeyOrchestration
	orchestrationRouterPrompt     = "" +
		"You are an orchestration router inside an agent workflow. " +
		"Return only valid JSON without markdown fences. " +
		"Use the shape " +
		"{\"mode\":string,\"use_memory\":boolean,\"memory_query\":string,\"needs_clarification\":boolean," +
		"\"clarification_question\":string,\"reasoning\":string,\"target_subgraph\":string}. " +
		"Valid mode values are direct, planner, supervisor. " +
		"Use needs_clarification=true only when missing information blocks safe progress."
)

type OrchestrationRouterNode struct {
	NodeInfo
	OrchestrationStatePath string
	InputPath              string
	StateScope             string
	ContextPaths           []string
	AvailableModes         []string
	Instructions           string
}

type orchestrationRouterResponse struct {
	Mode                  string `json:"mode"`
	UseMemory             bool   `json:"use_memory"`
	MemoryQuery           string `json:"memory_query"`
	NeedsClarification    bool   `json:"needs_clarification"`
	ClarificationQuestion string `json:"clarification_question"`
	Reasoning             string `json:"reasoning"`
	TargetSubgraph        string `json:"target_subgraph"`
}

func NewOrchestrationRouterNode() *OrchestrationRouterNode {
	id := uuid.New()
	return &OrchestrationRouterNode{
		NodeInfo: NodeInfo{
			NodeID:          "OrchestrationRouter_" + id.String(),
			NodeName:        "OrchestrationRouter",
			NodeDescription: "Decide whether to clarify, use memory, or route into planner/supervisor/direct execution.",
		},
		OrchestrationStatePath: defaultOrchestrationStatePath,
		AvailableModes:         []string{"direct", "planner", "supervisor"},
	}
}

func (n *OrchestrationRouterNode) Invoke(ctx context.Context, state fruntime.State) (fruntime.State, error) {
	svc := fruntime.ServicesFrom(ctx)
	if svc == nil || svc.Model == nil {
		return state, errors.New("orchestration router: model service not available")
	}
	if state == nil {
		state = fruntime.State{}
	}

	input, err := n.resolveInput(state)
	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "orchestration.error", map[string]any{"error": err.Error()})
		return state, err
	}

	orchestrationPath := n.effectiveOrchestrationStatePath()
	payload := map[string]any{
		"input":                    input,
		"orchestration_state":      existingOrchestrationState(state, orchestrationPath),
		"orchestration_state_path": orchestrationPath,
		"input_path":               strings.TrimSpace(n.InputPath),
		"state_scope":              strings.TrimSpace(n.StateScope),
		"context":                  n.collectContext(state),
		"available_modes":          cloneOrchestrationStrings(n.effectiveModes()),
		"additional_rules":         strings.TrimSpace(n.Instructions),
	}
	fruntime.NodeLogInfo(ctx, "orchestration router evaluating request",
		zap.String("orchestration_path", orchestrationPath),
		zap.String("input", input),
		zap.Int("available_modes", len(n.effectiveModes())),
		zap.Int("context_paths", len(n.collectContext(state))),
	)
	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "orchestration.prompt", payload)

	resp, err := svc.Model.GenerateContent(
		ctx,
		[]llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, orchestrationRouterPrompt),
			llms.TextParts(llms.ChatMessageTypeHuman, buildOrchestrationRouterPrompt(payload)),
		},
		llms.WithTemperature(0),
	)
	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "orchestration.error", map[string]any{"error": err.Error()})
		return state, err
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0] == nil {
		err = errors.New("orchestration router returned no choices")
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "orchestration.error", map[string]any{"error": err.Error()})
		return state, err
	}

	content := strings.TrimSpace(resp.Choices[0].Content)
	parsed, err := parseOrchestrationRouterResponse(content, n.effectiveModes())
	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "orchestration.error", map[string]any{
			"error":    err.Error(),
			"response": content,
		})
		return state, err
	}

	target, err := ensureOrchestrationStateAtPath(state, orchestrationPath)
	if err != nil {
		return state, err
	}
	target["mode"] = parsed.Mode
	target["use_memory"] = parsed.UseMemory
	target["memory_query"] = parsed.MemoryQuery
	target["needs_clarification"] = parsed.NeedsClarification
	target["clarification_question"] = parsed.ClarificationQuestion
	target["reasoning"] = parsed.Reasoning
	target["target_subgraph"] = parsed.TargetSubgraph

	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, map[string]any{
		"kind":                     "orchestration",
		"orchestration_state_path": orchestrationPath,
		"mode":                     parsed.Mode,
		"use_memory":               parsed.UseMemory,
		"needs_clarification":      parsed.NeedsClarification,
		"target_subgraph":          parsed.TargetSubgraph,
	})
	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "orchestration.response", parsed)
	fruntime.NodeLogInfo(ctx, "orchestration router decided route",
		zap.String("orchestration_path", orchestrationPath),
		zap.String("mode", parsed.Mode),
		zap.Bool("use_memory", parsed.UseMemory),
		zap.Bool("needs_clarification", parsed.NeedsClarification),
		zap.String("target_subgraph", parsed.TargetSubgraph),
	)

	return state, nil
}

func (n *OrchestrationRouterNode) GraphNodeSpec() dsl.GraphNodeSpec {
	config := map[string]any{
		"orchestration_state_path": n.effectiveOrchestrationStatePath(),
		"state_scope":              n.StateScope,
		"available_modes":          cloneOrchestrationStrings(n.effectiveModes()),
	}
	if inputPath := strings.TrimSpace(n.InputPath); inputPath != "" {
		config["input_path"] = inputPath
	}
	if len(n.ContextPaths) > 0 {
		config["context_paths"] = cloneOrchestrationStrings(n.ContextPaths)
	}
	if instructions := strings.TrimSpace(n.Instructions); instructions != "" {
		config["instructions"] = instructions
	}

	return dsl.GraphNodeSpec{
		ID:          n.ID(),
		Name:        n.Name(),
		Type:        "orchestration_router",
		Description: n.Description(),
		Config:      config,
	}
}

func (n *OrchestrationRouterNode) effectiveOrchestrationStatePath() string {
	if n == nil || strings.TrimSpace(n.OrchestrationStatePath) == "" {
		return defaultOrchestrationStatePath
	}
	return strings.TrimSpace(n.OrchestrationStatePath)
}

func (n *OrchestrationRouterNode) effectiveModes() []string {
	if n == nil || len(n.AvailableModes) == 0 {
		return []string{"direct", "planner", "supervisor"}
	}
	result := make([]string, 0, len(n.AvailableModes))
	seen := map[string]struct{}{}
	for _, mode := range n.AvailableModes {
		mode = normalizeOrchestrationMode(mode)
		if mode == "" {
			continue
		}
		if _, exists := seen[mode]; exists {
			continue
		}
		seen[mode] = struct{}{}
		result = append(result, mode)
	}
	if len(result) == 0 {
		return []string{"direct", "planner", "supervisor"}
	}
	return result
}

func (n *OrchestrationRouterNode) resolveInput(state fruntime.State) (string, error) {
	if inputPath := strings.TrimSpace(n.InputPath); inputPath != "" {
		value, ok := state.ResolvePath(inputPath)
		if !ok {
			return "", fmt.Errorf("orchestration input not found at %q", inputPath)
		}
		text := strings.TrimSpace(stringifyOrchestrationValue(value))
		if text == "" {
			return "", fmt.Errorf("orchestration input at %q is empty", inputPath)
		}
		return text, nil
	}

	messages := state.Conversation(n.StateScope).Messages()
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != llms.ChatMessageTypeHuman {
			continue
		}
		text := strings.TrimSpace(extractText(messages[i]))
		if text != "" {
			return text, nil
		}
	}
	return "", errors.New("orchestration input is empty: no configured input_path and no human message found")
}

func (n *OrchestrationRouterNode) collectContext(state fruntime.State) map[string]any {
	if len(n.ContextPaths) == 0 {
		return nil
	}

	contextPayload := make(map[string]any, len(n.ContextPaths))
	for _, path := range n.ContextPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		value, ok := state.ResolvePath(path)
		if !ok {
			continue
		}
		contextPayload[path] = value
	}
	if len(contextPayload) == 0 {
		return nil
	}
	return contextPayload
}

func buildOrchestrationRouterPrompt(payload map[string]any) string {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "Decide the orchestration strategy from the provided request payload."
	}
	return "Decide the orchestration strategy from the following JSON payload.\n\n" + string(data)
}

func parseOrchestrationRouterResponse(content string, allowedModes []string) (orchestrationRouterResponse, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return orchestrationRouterResponse{}, errors.New("orchestration router response is empty")
	}

	candidates := []string{content, stripOrchestrationCodeFence(content)}
	for _, candidate := range candidates {
		if parsed, err := decodeOrchestrationRouterResponse(candidate, allowedModes); err == nil {
			return parsed, nil
		}
	}

	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start >= 0 && end > start {
		return decodeOrchestrationRouterResponse(content[start:end+1], allowedModes)
	}
	return orchestrationRouterResponse{}, errors.New("orchestration router response is not valid JSON")
}

func decodeOrchestrationRouterResponse(content string, allowedModes []string) (orchestrationRouterResponse, error) {
	var parsed orchestrationRouterResponse
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return orchestrationRouterResponse{}, err
	}
	parsed = normalizeOrchestrationRouterResponse(parsed, allowedModes)
	if parsed.Mode == "" {
		return orchestrationRouterResponse{}, errors.New("orchestration router returned an empty mode")
	}
	return parsed, nil
}

func normalizeOrchestrationRouterResponse(parsed orchestrationRouterResponse, allowedModes []string) orchestrationRouterResponse {
	parsed.Mode = normalizeOrchestrationMode(parsed.Mode)
	if parsed.Mode == "" {
		parsed.Mode = "direct"
	}

	allowed := map[string]struct{}{}
	for _, mode := range allowedModes {
		mode = normalizeOrchestrationMode(mode)
		if mode == "" {
			continue
		}
		allowed[mode] = struct{}{}
	}
	if len(allowed) > 0 {
		if _, ok := allowed[parsed.Mode]; !ok {
			parsed.Mode = firstAllowedOrchestrationMode(allowedModes)
		}
	}

	parsed.MemoryQuery = strings.TrimSpace(parsed.MemoryQuery)
	parsed.ClarificationQuestion = strings.TrimSpace(parsed.ClarificationQuestion)
	parsed.Reasoning = strings.TrimSpace(parsed.Reasoning)
	parsed.TargetSubgraph = strings.TrimSpace(parsed.TargetSubgraph)
	if parsed.NeedsClarification && parsed.ClarificationQuestion == "" {
		parsed.ClarificationQuestion = "Could you clarify the missing information needed to proceed?"
	}
	return parsed
}

func firstAllowedOrchestrationMode(allowedModes []string) string {
	for _, mode := range allowedModes {
		mode = normalizeOrchestrationMode(mode)
		if mode != "" {
			return mode
		}
	}
	return "direct"
}

func normalizeOrchestrationMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "direct", "planner", "supervisor":
		return mode
	default:
		return ""
	}
}

func existingOrchestrationState(state fruntime.State, path string) map[string]any {
	value, ok := state.ResolvePath(path)
	if !ok {
		return nil
	}
	switch typed := value.(type) {
	case fruntime.State:
		return typed
	case map[string]any:
		return typed
	default:
		return nil
	}
}

func ensureOrchestrationStateAtPath(root fruntime.State, path string) (fruntime.State, error) {
	segments := fruntime.SplitStatePath(path)
	if len(segments) == 0 {
		return nil, errors.New("orchestration state path is required")
	}

	current := root
	for _, segment := range segments {
		switch typed := current[segment].(type) {
		case nil:
			nested := fruntime.State{}
			current[segment] = nested
			current = nested
		case fruntime.State:
			current = typed
		case map[string]any:
			nested := fruntime.State(typed)
			current[segment] = nested
			current = nested
		default:
			return nil, fmt.Errorf("orchestration state path %q contains non-object segment %q (%T)", path, segment, typed)
		}
	}
	return current, nil
}

func stringifyOrchestrationValue(value any) string {
	switch typed := value.(type) {
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

func stripOrchestrationCodeFence(content string) string {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "```") {
		return content
	}
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```JSON")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	return strings.TrimSpace(content)
}

func cloneOrchestrationStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	cloned := make([]string, len(items))
	copy(cloned, items)
	return cloned
}
