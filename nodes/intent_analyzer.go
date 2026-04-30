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
	defaultIntentStatePath = fruntime.StateKeyIntent
	intentAnalyzerPrompt   = "" +
		"You are an intent analysis node inside an agent workflow. " +
		"Return only valid JSON without markdown fences. " +
		"Use the shape " +
		"{\"label\":string,\"confidence\":number,\"reasoning\":string,\"slots\":object,\"candidates\":[{\"label\":string,\"confidence\":number,\"reasoning\":string}]}. " +
		"Confidence must be between 0 and 1. " +
		"If the intent is unclear, use label \"unknown\"."
)

type IntentAnalyzerNode struct {
	NodeInfo
	IntentStatePath string
	InputPath       string
	StateScope      string
	IntentOptions   []string
	Instructions    string
}

type intentAnalyzerResponse struct {
	Label      string                    `json:"label"`
	Confidence float64                   `json:"confidence"`
	Reasoning  string                    `json:"reasoning"`
	Slots      map[string]any            `json:"slots"`
	Candidates []intentAnalyzerCandidate `json:"candidates"`
}

type intentAnalyzerCandidate struct {
	Label      string  `json:"label"`
	Confidence float64 `json:"confidence"`
	Reasoning  string  `json:"reasoning"`
}

func NewIntentAnalyzerNode() *IntentAnalyzerNode {
	id := uuid.New()
	return &IntentAnalyzerNode{
		NodeInfo: NodeInfo{
			NodeID:          "IntentAnalyzer_" + id.String(),
			NodeName:        "IntentAnalyzer",
			NodeDescription: "Analyze the latest request and write a structured intent result into state.",
		},
		IntentStatePath: defaultIntentStatePath,
	}
}

func (n *IntentAnalyzerNode) Invoke(ctx context.Context, state fruntime.State) (fruntime.State, error) {
	svc := fruntime.ServicesFrom(ctx)
	if svc == nil || svc.Model == nil {
		return state, errors.New("intent analyzer: model service not available")
	}
	if state == nil {
		state = fruntime.State{}
	}

	input, err := n.resolveInput(state)
	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "intent.error", map[string]any{"error": err.Error()})
		return state, err
	}

	intentPath := n.effectiveIntentStatePath()
	payload := map[string]any{
		"input":             input,
		"intent_state_path": intentPath,
		"input_path":        strings.TrimSpace(n.InputPath),
		"state_scope":       strings.TrimSpace(n.StateScope),
		"intent_options":    cloneIntentStrings(n.IntentOptions),
		"additional_rules":  strings.TrimSpace(n.Instructions),
	}
	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "intent.prompt", payload)

	resp, err := svc.Model.GenerateContent(
		ctx,
		[]llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, intentAnalyzerPrompt),
			llms.TextParts(llms.ChatMessageTypeHuman, buildIntentAnalyzerPrompt(payload)),
		},
		llms.WithTemperature(0),
	)
	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "intent.error", map[string]any{"error": err.Error()})
		return state, err
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0] == nil {
		err = errors.New("intent analyzer returned no choices")
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "intent.error", map[string]any{"error": err.Error()})
		return state, err
	}

	content := strings.TrimSpace(resp.Choices[0].Content)
	parsed, err := parseIntentAnalyzerResponse(content)
	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "intent.error", map[string]any{
			"error":    err.Error(),
			"response": content,
		})
		return state, err
	}

	target, err := ensureIntentStateAtPath(state, intentPath)
	if err != nil {
		return state, err
	}
	target["label"] = parsed.Label
	target["confidence"] = parsed.Confidence
	target["reasoning"] = parsed.Reasoning
	target["slots"] = cloneIntentMap(parsed.Slots)
	if len(parsed.Candidates) > 0 {
		target["candidates"] = intentCandidatesToState(parsed.Candidates)
	} else {
		delete(target, "candidates")
	}

	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, map[string]any{
		"kind":              "intent",
		"intent_state_path": intentPath,
		"label":             parsed.Label,
		"confidence":        parsed.Confidence,
	})
	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "intent.response", parsed)

	return state, nil
}

func (n *IntentAnalyzerNode) GraphNodeSpec() dsl.GraphNodeSpec {
	config := map[string]any{
		"intent_state_path": n.effectiveIntentStatePath(),
		"state_scope":       n.StateScope,
	}
	if inputPath := strings.TrimSpace(n.InputPath); inputPath != "" {
		config["input_path"] = inputPath
	}
	if len(n.IntentOptions) > 0 {
		config["intent_options"] = cloneIntentStrings(n.IntentOptions)
	}
	if instructions := strings.TrimSpace(n.Instructions); instructions != "" {
		config["instructions"] = instructions
	}

	return dsl.GraphNodeSpec{
		ID:          n.ID(),
		Name:        n.Name(),
		Type:        "intent_analyzer",
		Description: n.Description(),
		Config:      config,
	}
}

func (n *IntentAnalyzerNode) effectiveIntentStatePath() string {
	if n == nil || strings.TrimSpace(n.IntentStatePath) == "" {
		return defaultIntentStatePath
	}
	return strings.TrimSpace(n.IntentStatePath)
}

func (n *IntentAnalyzerNode) resolveInput(state fruntime.State) (string, error) {
	if inputPath := strings.TrimSpace(n.InputPath); inputPath != "" {
		value, ok := fruntime.ResolveStatePath(state, inputPath)
		if !ok {
			return "", fmt.Errorf("intent input not found at %q", inputPath)
		}
		text := strings.TrimSpace(stringifyIntentValue(value))
		if text == "" {
			return "", fmt.Errorf("intent input at %q is empty", inputPath)
		}
		return text, nil
	}

	messages := fruntime.Conversation(state, n.StateScope).Messages()
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != llms.ChatMessageTypeHuman {
			continue
		}
		text := strings.TrimSpace(extractText(messages[i]))
		if text != "" {
			return text, nil
		}
	}
	return "", errors.New("intent input is empty: no configured input_path and no human message found")
}

func buildIntentAnalyzerPrompt(payload map[string]any) string {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "Analyze the intent from the provided request payload."
	}
	return "Analyze the intent from the following JSON payload.\n\n" + string(data)
}

func parseIntentAnalyzerResponse(content string) (intentAnalyzerResponse, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return intentAnalyzerResponse{}, errors.New("intent analyzer response is empty")
	}

	candidates := []string{content, stripIntentAnalyzerCodeFence(content)}
	for _, candidate := range candidates {
		if parsed, err := decodeIntentAnalyzerResponse(candidate); err == nil {
			return parsed, nil
		}
	}

	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start >= 0 && end > start {
		return decodeIntentAnalyzerResponse(content[start : end+1])
	}
	return intentAnalyzerResponse{}, errors.New("intent analyzer response is not valid JSON")
}

func decodeIntentAnalyzerResponse(content string) (intentAnalyzerResponse, error) {
	var parsed intentAnalyzerResponse
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return intentAnalyzerResponse{}, err
	}
	parsed = normalizeIntentAnalyzerResponse(parsed)
	if parsed.Label == "" {
		return intentAnalyzerResponse{}, errors.New("intent analyzer returned an empty label")
	}
	return parsed, nil
}

func normalizeIntentAnalyzerResponse(parsed intentAnalyzerResponse) intentAnalyzerResponse {
	parsed.Label = strings.TrimSpace(parsed.Label)
	if parsed.Label == "" {
		parsed.Label = "unknown"
	}
	parsed.Confidence = normalizeIntentConfidence(parsed.Confidence)
	parsed.Reasoning = strings.TrimSpace(parsed.Reasoning)
	if len(parsed.Slots) == 0 {
		parsed.Slots = nil
	}

	normalizedCandidates := make([]intentAnalyzerCandidate, 0, len(parsed.Candidates))
	for _, candidate := range parsed.Candidates {
		candidate.Label = strings.TrimSpace(candidate.Label)
		if candidate.Label == "" {
			continue
		}
		candidate.Confidence = normalizeIntentConfidence(candidate.Confidence)
		candidate.Reasoning = strings.TrimSpace(candidate.Reasoning)
		normalizedCandidates = append(normalizedCandidates, candidate)
	}
	parsed.Candidates = normalizedCandidates
	return parsed
}

func normalizeIntentConfidence(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func ensureIntentStateAtPath(root fruntime.State, path string) (fruntime.State, error) {
	segments := fruntime.SplitStatePath(path)
	if len(segments) == 0 {
		return nil, errors.New("intent state path is required")
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
			return nil, fmt.Errorf("intent state path %q contains non-object segment %q (%T)", path, segment, typed)
		}
	}
	return current, nil
}

func stringifyIntentValue(value any) string {
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

func stripIntentAnalyzerCodeFence(content string) string {
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

func cloneIntentStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	cloned := make([]string, len(items))
	copy(cloned, items)
	return cloned
}

func cloneIntentMap(items map[string]any) map[string]any {
	if len(items) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(items))
	for key, value := range items {
		cloned[key] = value
	}
	return cloned
}

func intentCandidatesToState(candidates []intentAnalyzerCandidate) []map[string]any {
	if len(candidates) == 0 {
		return nil
	}
	items := make([]map[string]any, 0, len(candidates))
	for _, candidate := range candidates {
		items = append(items, map[string]any{
			"label":      candidate.Label,
			"confidence": candidate.Confidence,
			"reasoning":  candidate.Reasoning,
		})
	}
	return items
}
