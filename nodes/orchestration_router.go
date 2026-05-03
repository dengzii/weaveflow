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

const (
	defaultOrchestrationStatePath = fruntime.StateKeyOrchestration
	orchestrationRouterPrompt     = `You are an orchestration router inside an agent workflow.
Return JSON only. Do not use markdown fences.

Required keys and types:
- mode: string
- use_memory: boolean
- memory_query: string
- needs_clarification: boolean
- clarification_question: string
- reasoning: string
- target_subgraph: string
- direct_answer: string

Rules:
- Valid mode values: direct, planner, supervisor.
- Prefer direct when one assistant turn can safely complete the request.
- Use planner only for multi-step decomposition, tool-heavy work, or execution that must be checked step by step.
- Use supervisor only for explicit multi-agent delegation or handoff.
- Use memory only when prior session context is required.
- Set needs_clarification=true only when missing information blocks safe progress.
- If mode is direct and you can answer immediately, put the full user-facing reply in direct_answer.
- Keep reasoning brief.`
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
	DirectAnswer          string `json:"direct_answer"`
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
	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "orchestration.prompt", payload)

	resp, err := svc.Model.GenerateContent(
		ctx,
		[]llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, orchestrationRouterPrompt),
			llms.TextParts(llms.ChatMessageTypeHuman, buildOrchestrationRouterPrompt(payload)),
		},
		llms.WithJSONMode(),
		llms.WithThinkingMode(llms.ThinkingModeNone),
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
	target["direct_answer"] = parsed.DirectAnswer
	n.applyDirectAnswer(state, parsed.DirectAnswer)

	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, map[string]any{
		"kind":                     "orchestration",
		"orchestration_state_path": orchestrationPath,
		"mode":                     parsed.Mode,
		"use_memory":               parsed.UseMemory,
		"needs_clarification":      parsed.NeedsClarification,
		"target_subgraph":          parsed.TargetSubgraph,
		"has_direct_answer":        parsed.DirectAnswer != "",
	})
	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "orchestration.response", parsed)

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
	data, err := json.Marshal(buildOrchestrationPromptPayload(payload))
	if err != nil {
		return "Decide the orchestration strategy from the provided request payload."
	}
	return "Route the request from this JSON payload and answer immediately when direct is enough.\n" + string(data)
}

func buildOrchestrationPromptPayload(payload map[string]any) map[string]any {
	compact := compactOrchestrationPromptValue(map[string]any{
		"request":         payload["input"],
		"prior_route":     payload["orchestration_state"],
		"context":         payload["context"],
		"available_modes": payload["available_modes"],
		"rules":           payload["additional_rules"],
	})
	if object, ok := compact.(map[string]any); ok {
		return object
	}
	return map[string]any{}
}

func compactOrchestrationPromptValue(value any) any {
	switch typed := value.(type) {
	case fruntime.State:
		return compactOrchestrationPromptValue(map[string]any(typed))
	case map[string]any:
		compacted := make(map[string]any, len(typed))
		for key, item := range typed {
			compactedItem := compactOrchestrationPromptValue(item)
			if compactedItem == nil {
				continue
			}
			compacted[key] = compactedItem
		}
		if len(compacted) == 0 {
			return nil
		}
		return compacted
	case []string:
		compacted := make([]string, 0, len(typed))
		for _, item := range typed {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			compacted = append(compacted, item)
		}
		if len(compacted) == 0 {
			return nil
		}
		return compacted
	case []any:
		compacted := make([]any, 0, len(typed))
		for _, item := range typed {
			compactedItem := compactOrchestrationPromptValue(item)
			if compactedItem == nil {
				continue
			}
			compacted = append(compacted, compactedItem)
		}
		if len(compacted) == 0 {
			return nil
		}
		return compacted
	case string:
		typed = strings.TrimSpace(typed)
		if typed == "" {
			return nil
		}
		return typed
	default:
		return value
	}
}

func parseOrchestrationRouterResponse(content string, allowedModes []string) (orchestrationRouterResponse, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return orchestrationRouterResponse{}, errors.New("orchestration router response is empty")
	}

	stripped := stripOrchestrationCodeFence(content)
	candidates := uniqueOrchestrationCandidates([]string{
		content,
		stripped,
		extractOrchestrationJSONObject(content, strings.Index(content, "{")),
		extractOrchestrationJSONObject(stripped, strings.Index(stripped, "{")),
		salvageOrchestrationJSON(content),
		salvageOrchestrationJSON(stripped),
	})
	for _, candidate := range candidates {
		if parsed, err := decodeOrchestrationRouterResponse(candidate, allowedModes); err == nil {
			return parsed, nil
		}
	}

	if repaired := salvageOrchestrationJSON(content); repaired != "" {
		return decodeOrchestrationRouterResponse(repaired, allowedModes)
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

	allowed := allowedOrchestrationModes(allowedModes)
	if len(allowed) > 0 {
		if _, ok := allowed[parsed.Mode]; !ok {
			parsed.Mode = firstAllowedOrchestrationMode(allowedModes)
		}
	}

	parsed.MemoryQuery = strings.TrimSpace(parsed.MemoryQuery)
	parsed.ClarificationQuestion = strings.TrimSpace(parsed.ClarificationQuestion)
	parsed.Reasoning = strings.TrimSpace(parsed.Reasoning)
	parsed.TargetSubgraph = strings.TrimSpace(parsed.TargetSubgraph)
	parsed.DirectAnswer = strings.TrimSpace(parsed.DirectAnswer)
	if parsed.DirectAnswer != "" {
		if len(allowed) > 0 {
			if _, ok := allowed["direct"]; !ok {
				parsed.DirectAnswer = ""
			}
		}
		if parsed.DirectAnswer != "" {
			parsed.Mode = "direct"
			parsed.UseMemory = false
			parsed.MemoryQuery = ""
			parsed.NeedsClarification = false
			parsed.ClarificationQuestion = ""
			parsed.TargetSubgraph = ""
		}
	}
	if parsed.NeedsClarification && parsed.ClarificationQuestion == "" {
		parsed.ClarificationQuestion = "Could you clarify the missing information needed to proceed?"
	}
	return parsed
}

func (n *OrchestrationRouterNode) applyDirectAnswer(state fruntime.State, answer string) {
	answer = strings.TrimSpace(answer)
	if answer == "" || state == nil {
		return
	}

	conversation := state.Conversation(n.StateScope)
	messages := conversation.Messages()
	if !lastOrchestrationMessageMatches(messages, answer) {
		messages = append(messages, llms.TextParts(llms.ChatMessageTypeAI, answer))
		conversation.UpdateMessage(messages)
	}
	conversation.SetFinalAnswer(answer)
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

func allowedOrchestrationModes(allowedModes []string) map[string]struct{} {
	allowed := map[string]struct{}{}
	for _, mode := range allowedModes {
		mode = normalizeOrchestrationMode(mode)
		if mode == "" {
			continue
		}
		allowed[mode] = struct{}{}
	}
	return allowed
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

func lastOrchestrationMessageMatches(messages []llms.MessageContent, answer string) bool {
	if len(messages) == 0 {
		return false
	}
	last := messages[len(messages)-1]
	if last.Role != llms.ChatMessageTypeAI {
		return false
	}
	return strings.TrimSpace(extractText(last)) == answer
}

func uniqueOrchestrationCandidates(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	result := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result
}

func salvageOrchestrationJSON(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}

	markers := []string{
		`{"mode"`,
		`{"use_memory"`,
		`{"needs_clarification"`,
		`{"clarification_question"`,
		`{"direct_answer"`,
	}
	best := -1
	for _, marker := range markers {
		index := strings.Index(content, marker)
		if index < 0 {
			continue
		}
		if best < 0 || index < best {
			best = index
		}
	}
	if best <= 0 {
		return ""
	}
	return extractOrchestrationJSONObject(content, best)
}

func extractOrchestrationJSONObject(content string, start int) string {
	content = strings.TrimSpace(content)
	if start < 0 || start >= len(content) || content[start] != '{' {
		return ""
	}

	depth := 0
	inString := false
	escaped := false
	for index := start; index < len(content); index++ {
		ch := content[index]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch ch {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}

		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return strings.TrimSpace(content[start : index+1])
			}
		}
	}
	return ""
}
