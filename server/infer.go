package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	llama_cpp "falcon/llama.cpp"

	"github.com/gin-gonic/gin"
)

type Infer struct {
}

type loadedModel struct {
	info  ModelInfo
	path  string
	model *llama_cpp.Model
}

var inferModels = struct {
	sync.RWMutex
	items map[string]*loadedModel
}{
	items: map[string]*loadedModel{},
}

func (r Infer) ReleaseModel(ctx *gin.Context) error {
	id := ctx.Param("id")
	if strings.TrimSpace(id) == "" {
		return errorInvalidParam
	}

	inferModels.Lock()
	defer inferModels.Unlock()

	item, ok := inferModels.items[id]
	if !ok {
		return fmt.Errorf("model %q not found", id)
	}

	delete(inferModels.items, id)
	if item.model != nil {
		_ = item.model.Close()
	}

	return responseSuccess(ctx, gin.H{"id": id})
}

func (r Infer) LoadModel(ctx *gin.Context, param *LoadModelRequest) error {
	backend := normalizeBackend(param.Backend)
	if backend != "llama.cpp" {
		return fmt.Errorf("unsupported backend %q", param.Backend)
	}

	model, err := llama_cpp.Load(param.Path, llama_cpp.LoadOptions{})
	if err != nil {
		return err
	}

	id := inferModelID(param.Path)
	item := &loadedModel{
		info: ModelInfo{
			Id:          id,
			Object:      "model",
			OwnedBy:     "falcon",
			Backend:     backend,
			LastUpdated: model.LoadedAt(),
		},
		path:  param.Path,
		model: model,
	}

	inferModels.Lock()
	if prev, ok := inferModels.items[id]; ok && prev.model != nil {
		_ = prev.model.Close()
	}
	inferModels.items[id] = item
	inferModels.Unlock()

	return responseSuccess(ctx, item.info)
}

func (r Infer) ModelList(ctx *gin.Context) error {
	inferModels.RLock()
	models := make([]ModelInfo, 0, len(inferModels.items))
	for _, item := range inferModels.items {
		models = append(models, item.info)
	}
	inferModels.RUnlock()

	sort.Slice(models, func(i, j int) bool {
		return models[i].Id < models[j].Id
	})

	return responseSuccess(ctx, models)
}

func (r Infer) Chat(ctx *gin.Context, request *ChatRequest) error {
	model, modelID, err := resolveChatModel(request.Model)
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

	if request.Stream {
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

		resultCh, errCh := model.Generate(ctx.Request.Context(), prompt, llama_cpp.GenerateOptions{
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

	resultCh, errCh := model.Generate(ctx.Request.Context(), prompt, llama_cpp.GenerateOptions{
		MaxTokens:   request.MaxTokens,
		Temperature: request.Temperature,
		TopP:        request.TopP,
		TopK:        int(request.TopK),
		Stop:        request.Stop,
		AddSpecial:  true,
	})
	result, err := llama_cpp.Collect(resultCh, errCh)
	if err != nil {
		return err
	}

	return responseSuccess(ctx, gin.H{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   modelID,
		"choices": []gin.H{
			{
				"index": 0,
				"message": gin.H{
					"role":    "assistant",
					"content": result.Content,
				},
				"finish_reason": finishReasonFromStopReason(result.StopReason),
				"stop_reason":   result.StopReason,
				"token_count":   result.TokenCount,
			},
		},
	})
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
	case "", "llama.cpp", "llamacpp":
		return "llama.cpp"
	default:
		return strings.TrimSpace(backend)
	}
}

func inferModelID(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}

func resolveChatModel(requestedID string) (*llama_cpp.Model, string, error) {
	inferModels.RLock()
	defer inferModels.RUnlock()

	if requestedID != "" {
		item, ok := inferModels.items[requestedID]
		if !ok {
			return nil, "", fmt.Errorf("model %q not found", requestedID)
		}
		return item.model, requestedID, nil
	}

	if len(inferModels.items) == 1 {
		for id, item := range inferModels.items {
			return item.model, id, nil
		}
	}

	if len(inferModels.items) == 0 {
		return nil, "", errors.New("no model loaded")
	}

	return nil, "", errors.New("multiple models loaded, request.model is required")
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
