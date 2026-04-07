package nodes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"falcon/llama_cpp"
	"falcon/runtime"

	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
)

const (
	defaultInputKey      = "input"
	defaultOutputKey     = "output"
	defaultReasoningKey  = "reasoning"
	defaultStopReasonKey = "stop_reason"
	defaultTokenCountKey = "token_count"
)

type llamaContentModel interface {
	GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error)
	Release() error
}

type llamaModelLoader func(path string, opts llama_cpp.LoadOptions) (llamaContentModel, error)

type LlamaCppModel struct {
	NodeInfo

	ModelPath       string
	LoadOptions     llama_cpp.LoadOptions
	GenerateOptions llama_cpp.GenerateOptions

	StateScope    string
	InputKey      string
	OutputKey     string
	ReasoningKey  string
	StopReasonKey string
	TokenCountKey string
	SystemPrompt  string

	ReturnThinking bool
	StreamThinking bool
	ThinkingMode   llms.ThinkingMode
	ThinkingBudget int

	mu         sync.Mutex
	model      llamaContentModel
	loadedPath string
	loader     llamaModelLoader
}

func NewLlamaCppModel(modelPath string) *LlamaCppModel {
	id := uuid.New()
	return &LlamaCppModel{
		NodeInfo: NodeInfo{
			NodeID:          id.String(),
			NodeName:        "llama.cpp Node",
			NodeDescription: "Load a local llama.cpp model and run inference.",
		},
		ModelPath:     strings.TrimSpace(modelPath),
		InputKey:      defaultInputKey,
		OutputKey:     defaultOutputKey,
		ReasoningKey:  defaultReasoningKey,
		StopReasonKey: defaultStopReasonKey,
		TokenCountKey: defaultTokenCountKey,
	}
}

func (l *LlamaCppModel) ID() string {
	if l == nil {
		return "llama_cpp"
	}
	if strings.TrimSpace(l.NodeID) == "" {
		return "llama_cpp"
	}
	return l.NodeInfo.ID()
}

func (l *LlamaCppModel) Name() string {
	if l == nil {
		return "llama.cpp Node"
	}
	if strings.TrimSpace(l.NodeName) == "" {
		return "llama.cpp Node"
	}
	return l.NodeInfo.Name()
}

func (l *LlamaCppModel) Description() string {
	if l == nil {
		return "Load a local llama.cpp model and run inference."
	}
	if strings.TrimSpace(l.NodeDescription) == "" {
		return "Load a local llama.cpp model and run inference."
	}
	return l.NodeInfo.Description()
}

func (l *LlamaCppModel) Invoke(ctx context.Context, state runtime.State) (runtime.State, error) {
	if l == nil {
		return state, errors.New("llama.cpp nodes is nil")
	}
	if state == nil {
		state = runtime.State{}
	}

	model, err := l.ensureModel()
	if err != nil {
		return state, err
	}

	messages, prompt, err := l.resolveMessages(state)
	if err != nil {
		return state, err
	}
	if payload, artifactErr := buildPromptArtifact(messages, l.StateScope, l.InputKey, prompt); artifactErr == nil {
		_, _ = runtime.SaveJSONArtifactBestEffort(ctx, "llama_cpp.prompt", payload)
	}

	resp, err := model.GenerateContent(ctx, messages, l.callOptions()...)
	if err != nil {
		_, _ = runtime.SaveJSONArtifactBestEffort(ctx, "llama_cpp.error", map[string]any{"error": err.Error()})
		return state, err
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0] == nil {
		err := errors.New("llama.cpp returned no choices")
		_, _ = runtime.SaveJSONArtifactBestEffort(ctx, "llama_cpp.error", map[string]any{"error": err.Error()})
		return state, err
	}

	choice := resp.Choices[0]
	output := strings.TrimSpace(choice.Content)
	if output == "" {
		output = strings.TrimSpace(choice.ReasoningContent)
	}
	reasoning := strings.TrimSpace(choice.ReasoningContent)
	stopReason, tokenCount := extractGenerationInfo(choice)
	usage := Extract(choice)
	record := RecordState(state, Record{
		NodeID:             l.ID(),
		Model:              l.modelLabel(),
		StateScope:         l.StateScope,
		StopReason:         stopReason,
		PromptTokens:       usage.PromptTokens,
		CompletionTokens:   usage.CompletionTokens,
		TotalTokens:        usage.TotalTokens,
		ReasoningTokens:    usage.ReasoningTokens,
		PromptCachedTokens: usage.PromptCachedTokens,
	})
	_ = PublishEvent(ctx, record)

	_, _ = runtime.SaveJSONArtifactBestEffort(ctx, "llama_cpp.response", map[string]any{
		"content":     output,
		"reasoning":   reasoning,
		"stop_reason": stopReason,
		"token_count": tokenCount,
		"state_scope": l.StateScope,
		"output_key":  l.effectiveOutputKey(),
		"usage":       record.ArtifactPayload(),
		"reasoning_key": func() string {
			if reasoning == "" {
				return ""
			}
			return l.effectiveReasoningKey()
		}(),
	})

	conversation := runtime.Conversation(state, l.StateScope)
	existing := conversation.Messages()
	if len(existing) == 0 {
		existing = append(existing, messages...)
	}
	if output != "" {
		conversation.UpdateMessage(append(existing, llms.TextParts(llms.ChatMessageTypeAI, output)))
		conversation.SetFinalAnswer(output)
	}

	target := l.outputState(state)
	target[l.effectiveOutputKey()] = output
	if reasoning != "" {
		target[l.effectiveReasoningKey()] = reasoning
	}
	if stopReason != "" {
		target[l.effectiveStopReasonKey()] = stopReason
	}
	if tokenCount > 0 {
		target[l.effectiveTokenCountKey()] = tokenCount
	}

	return state, nil
}

func (l *LlamaCppModel) Release() error {
	if l == nil {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.model == nil {
		l.loadedPath = ""
		return nil
	}

	err := l.model.Release()
	l.model = nil
	l.loadedPath = ""
	return err
}

func (l *LlamaCppModel) ensureModel() (llamaContentModel, error) {
	path := strings.TrimSpace(l.ModelPath)
	if path == "" {
		return nil, errors.New("llama.cpp nodes model path is required")
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.model != nil && l.loadedPath == path {
		return l.model, nil
	}

	if l.model != nil {
		_ = l.model.Release()
		l.model = nil
		l.loadedPath = ""
	}

	loader := l.loader
	if loader == nil {
		loader = func(path string, opts llama_cpp.LoadOptions) (llamaContentModel, error) {
			return llama_cpp.Load(path, opts)
		}
	}

	model, err := loader(path, l.LoadOptions)
	if err != nil {
		return nil, fmt.Errorf("load llama.cpp model: %w", err)
	}

	l.model = model
	l.loadedPath = path
	return model, nil
}

func (l *LlamaCppModel) resolveMessages(state runtime.State) ([]llms.MessageContent, string, error) {
	conversation := runtime.Conversation(state, l.StateScope)
	messages := conversation.Messages()
	if len(messages) > 0 {
		return l.withSystemPrompt(messages), "", nil
	}

	prompt := strings.TrimSpace(l.lookupPrompt(state))
	if prompt == "" {
		return nil, "", fmt.Errorf("llama.cpp nodes prompt is required: input key %q is empty", l.effectiveInputKey())
	}

	messages = []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, prompt)}
	return l.withSystemPrompt(messages), prompt, nil
}

func (l *LlamaCppModel) withSystemPrompt(messages []llms.MessageContent) []llms.MessageContent {
	cloned := cloneMessages(messages)
	systemPrompt := strings.TrimSpace(l.SystemPrompt)
	if systemPrompt == "" || hasSystemMessage(cloned) {
		return cloned
	}

	return append([]llms.MessageContent{llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt)}, cloned...)
}

func (l *LlamaCppModel) lookupPrompt(state runtime.State) string {
	key := l.effectiveInputKey()
	if key == "" || state == nil {
		return ""
	}

	if l.StateScope != "" {
		if scope := state.Scope(l.StateScope); scope != nil {
			if value, ok := stringValue(scope[key]); ok {
				return value
			}
		}
	}

	if value, ok := stringValue(state[key]); ok {
		return value
	}
	return ""
}

func (l *LlamaCppModel) outputState(state runtime.State) runtime.State {
	if state == nil {
		state = runtime.State{}
	}
	if l.StateScope == "" {
		return state
	}
	if scoped := state.EnsureScope(l.StateScope); scoped != nil {
		return scoped
	}
	return state
}

func (l *LlamaCppModel) callOptions() []llms.CallOption {
	options := []llms.CallOption{
		llms.WithStreamingReasoningFunc(l.onStreamingResponse),
	}

	if maxTokens := l.GenerateOptions.MaxTokens; maxTokens > 0 {
		options = append(options, llms.WithMaxTokens(maxTokens))
	}
	if temperature := l.GenerateOptions.Temperature; temperature != 0 {
		options = append(options, llms.WithTemperature(float64(temperature)))
	}
	if topP := l.GenerateOptions.TopP; topP > 0 {
		options = append(options, llms.WithTopP(float64(topP)))
	}
	if topK := l.GenerateOptions.TopK; topK > 0 {
		options = append(options, llms.WithTopK(topK))
	}
	if seed := l.GenerateOptions.Seed; seed != 0 {
		options = append(options, llms.WithSeed(int(seed)))
	}
	if len(l.GenerateOptions.Stop) > 0 {
		options = append(options, llms.WithStopWords(append([]string(nil), l.GenerateOptions.Stop...)))
	}
	if l.ReturnThinking {
		options = append(options, llms.WithReturnThinking(true))
	}
	if l.StreamThinking {
		options = append(options, llms.WithStreamThinking(true))
	}
	if l.ThinkingMode != "" {
		options = append(options, llms.WithThinkingMode(l.ThinkingMode))
	}
	if l.ThinkingBudget > 0 {
		options = append(options, llms.WithThinkingBudget(l.ThinkingBudget))
	}

	return options
}

func (l *LlamaCppModel) onStreamingResponse(ctx context.Context, reasoningChunk, chunk []byte) error {
	reasoning := string(reasoningChunk)
	if strings.TrimSpace(reasoning) != "" {
		if err := runtime.PublishRunnerContextEvent(ctx, runtime.EventLLMReasoningChunk, map[string]any{"text": reasoning}); err != nil {
			return err
		}
		if !runtime.HasRunnerEventPublisher(ctx) {
			fmt.Print(reasoning)
		}
	}

	content := string(chunk)
	if strings.TrimSpace(content) != "" {
		if err := runtime.PublishRunnerContextEvent(ctx, runtime.EventLLMContentChunk, map[string]any{"text": content}); err != nil {
			return err
		}
		if !runtime.HasRunnerEventPublisher(ctx) {
			fmt.Print(content)
		}
	}

	return nil
}

func (l *LlamaCppModel) effectiveInputKey() string {
	if strings.TrimSpace(l.InputKey) == "" {
		return defaultInputKey
	}
	return strings.TrimSpace(l.InputKey)
}

func (l *LlamaCppModel) effectiveOutputKey() string {
	if strings.TrimSpace(l.OutputKey) == "" {
		return defaultOutputKey
	}
	return strings.TrimSpace(l.OutputKey)
}

func (l *LlamaCppModel) effectiveReasoningKey() string {
	if strings.TrimSpace(l.ReasoningKey) == "" {
		return defaultReasoningKey
	}
	return strings.TrimSpace(l.ReasoningKey)
}

func (l *LlamaCppModel) effectiveStopReasonKey() string {
	if strings.TrimSpace(l.StopReasonKey) == "" {
		return defaultStopReasonKey
	}
	return strings.TrimSpace(l.StopReasonKey)
}

func (l *LlamaCppModel) effectiveTokenCountKey() string {
	if strings.TrimSpace(l.TokenCountKey) == "" {
		return defaultTokenCountKey
	}
	return strings.TrimSpace(l.TokenCountKey)
}

func (l *LlamaCppModel) modelLabel() string {
	if name := strings.TrimSpace(l.ModelPath); name != "" {
		name = strings.ReplaceAll(name, "\\", "/")
		if idx := strings.LastIndex(name, "/"); idx >= 0 && idx+1 < len(name) {
			name = name[idx+1:]
		}
		return name
	}
	return l.Name()
}

func extractGenerationInfo(choice *llms.ContentChoice) (string, int) {
	if choice == nil {
		return "", 0
	}

	stopReason := strings.TrimSpace(choice.StopReason)
	tokenCount := 0
	if len(choice.GenerationInfo) == 0 {
		return stopReason, tokenCount
	}

	if stopReason == "" {
		if value, ok := choice.GenerationInfo["StopReason"].(string); ok {
			stopReason = strings.TrimSpace(value)
		}
	}
	if value, ok := intValue(choice.GenerationInfo["CompletionTokens"]); ok {
		tokenCount = value
	} else if value, ok := intValue(choice.GenerationInfo["TotalTokens"]); ok {
		tokenCount = value
	}

	return stopReason, tokenCount
}

func buildPromptArtifact(messages []llms.MessageContent, stateScope string, inputKey string, prompt string) (map[string]any, error) {
	serialized, err := runtime.SerializeMessages(messages)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"state_scope": stateScope,
		"input_key":   strings.TrimSpace(inputKey),
		"prompt":      strings.TrimSpace(prompt),
		"messages":    serialized,
	}, nil
}

func hasSystemMessage(messages []llms.MessageContent) bool {
	for _, message := range messages {
		if message.Role == llms.ChatMessageTypeSystem {
			return true
		}
	}
	return false
}

func cloneMessages(messages []llms.MessageContent) []llms.MessageContent {
	if len(messages) == 0 {
		return nil
	}

	cloned := make([]llms.MessageContent, len(messages))
	for i, message := range messages {
		cloned[i] = llms.MessageContent{
			Role:  message.Role,
			Parts: append([]llms.ContentPart(nil), message.Parts...),
		}
	}
	return cloned
}

func stringValue(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case fmt.Stringer:
		return typed.String(), true
	case []byte:
		return string(typed), true
	default:
		return "", false
	}
}

func intValue(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int8:
		return int(typed), true
	case int16:
		return int(typed), true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case uint:
		return int(typed), true
	case uint8:
		return int(typed), true
	case uint16:
		return int(typed), true
	case uint32:
		return int(typed), true
	case uint64:
		return int(typed), true
	case float32:
		return int(typed), true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}
