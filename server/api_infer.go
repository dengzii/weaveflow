package server

import (
	"encoding/json"
	"errors"
	"falcon/llama_cpp"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type interApi struct {
	modelManager ModelManager
	mu           sync.RWMutex
	items        map[string]*loadedModel
}

type loadedModel struct {
	info ModelInfo
	path string
}

func (r *interApi) ReleaseModel(ctx *gin.Context) error {
	id := ctx.Param("id")
	if strings.TrimSpace(id) == "" {
		return errorInvalidParam
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	_, ok := r.items[id]
	if !ok {
		return fmt.Errorf("model %q not found", id)
	}

	if err := r.modelManager.Release(id); err != nil {
		return err
	}
	delete(r.items, id)

	return responseSuccess(ctx, gin.H{"id": id})
}

func (r *interApi) LoadModel(ctx *gin.Context, param *LoadModelRequest) error {
	backend := normalizeBackend(param.Backend)
	if backend != "llama_cpp" {
		return fmt.Errorf("unsupported backend %q", param.Backend)
	}

	id := inferModelID(param.Path)
	if err := r.modelManager.Load(id, param.Path, backend); err != nil {
		return err
	}

	item := &loadedModel{
		info: ModelInfo{
			Id:          id,
			Object:      "model",
			OwnedBy:     "falcon",
			Backend:     backend,
			LastUpdated: time.Now(),
		},
		path: param.Path,
	}

	r.mu.Lock()
	r.items[id] = item
	r.mu.Unlock()

	return responseSuccess(ctx, item.info)
}

func (r *interApi) ModelList(ctx *gin.Context) error {
	r.mu.RLock()
	models := make([]ModelInfo, 0, len(r.items))
	for _, item := range r.items {
		models = append(models, item.info)
	}
	r.mu.RUnlock()

	sort.Slice(models, func(i, j int) bool {
		return models[i].Id < models[j].Id
	})

	return responseSuccess(ctx, models)
}

func (r *interApi) Chat(ctx *gin.Context, request *ChatRequest) error {
	if !request.Stream {
		return errors.New("only support stream chat yet")
	}

	modelID, err := r.resolveChatModel(request.Model)
	if err != nil {
		return err
	}

	prompt, err := buildPrompt(request)
	if err != nil {
		return err
	}
	if strings.TrimSpace(prompt) == "" {
		return errors.New("messages are required")
	}

	prepareSSE(ctx)

	err = writeSSE(ctx, gin.H{
		"object": "chat.completion.chunk",
		"model":  modelID,
		"choices": []gin.H{
			{
				"index": 0,
				"delta": gin.H{"role": "assistant"},
			},
		},
	})
	if err != nil {
		return err
	}

	resultCh, errCh := r.modelManager.Generate(ctx.Request.Context(), modelID, prompt, llama_cpp.GenerateOptions{
		MaxTokens:   request.MaxTokens,
		Temperature: request.Temperature,
		TopP:        request.TopP,
		TopK:        int(request.TopK),
		Stop:        request.Stop,
		AddSpecial:  true,
	})

	finalResult := llama_cpp.GenerateResult{StopReason: llama_cpp.StopReasonNone}
	for result := range resultCh {
		if result.Content != "" {
			if err := writeSSE(ctx, gin.H{
				"object": "chat.completion.chunk",
				"model":  modelID,
				"choices": []gin.H{
					{
						"index": 0,
						"delta": gin.H{"content": result.Content},
					},
				},
			}); err != nil {
				return err
			}
		}
		if result.StopReason != llama_cpp.StopReasonNone {
			finalResult = result
		}
	}

	if err, ok := <-errCh; ok && err != nil {
		return err
	}

	if err := writeSSE(ctx, gin.H{
		"object": "chat.completion.chunk",
		"model":  modelID,
		"choices": []gin.H{
			{
				"index":         0,
				"delta":         gin.H{},
				"finish_reason": finishReasonFromStopReason(finalResult.StopReason),
			},
		},
	}); err != nil {
		return err
	}

	_, _ = fmt.Fprint(ctx.Writer, "data: [DONE]\n\n")
	ctx.Writer.Flush()
	return nil
}

func finishReasonFromStopReason(stopReason string) string {
	switch stopReason {
	case llama_cpp.StopReasonMaxTokens, llama_cpp.StopReasonContextFull:
		return "length"
	case llama_cpp.StopReasonCancelled:
		return "cancelled"
	case llama_cpp.StopReasonNone:
		return ""
	default:
		return "stop"
	}
}

func normalizeBackend(backend string) string {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "", "llama_cpp", "llama":
		return "llama_cpp"
	default:
		return strings.TrimSpace(backend)
	}
}

func inferModelID(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}

func (r *interApi) resolveChatModel(requestedID string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if requestedID != "" {
		_, ok := r.items[requestedID]
		if !ok {
			return "", fmt.Errorf("model %q not found", requestedID)
		}
		return requestedID, nil
	}

	if len(r.items) == 1 {
		for id := range r.items {
			return id, nil
		}
	}

	if len(r.items) == 0 {
		return "", errors.New("no model loaded")
	}

	return "", errors.New("multiple models loaded, request.model is required")
}

func buildPrompt(request *ChatRequest) (string, error) {
	messages := request.Message

	var b strings.Builder
	for _, msg := range messages {
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "user"
		}
		switch role {
		case "system":
			b.WriteString("System: ")
		case "assistant":
			b.WriteString("Assistant: ")
		case "user":
			b.WriteString("User: ")
		default:
			return "", errors.New(fmt.Sprintf("unsupported role: %q", role))
		}
		b.WriteString(strings.TrimSpace(msg.Content))
		b.WriteString("\n\n")
	}
	if b.Len() > 0 {
		b.WriteString("Assistant: ")
		if len(request.ReasoningEffort) > 0 && request.ReasoningEffort != "none" {
			b.WriteString("<think>")
		}
	}
	return b.String(), nil
}

func prepareSSE(ctx *gin.Context) {
	ctx.Status(http.StatusOK)
	ctx.Header("Content-Type", "text/event-stream")
	ctx.Header("Text-Type", "text/event-stream")
	ctx.Header("Cache-Control", "no-cache")
	ctx.Header("Connection", "keep-alive")
	ctx.Header("X-Accel-Buffering", "no")
}

func writeSSE(ctx *gin.Context, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintf(ctx.Writer, "data: %s\n\n", data); err != nil {
		return err
	}
	ctx.Writer.Flush()
	return nil
}
