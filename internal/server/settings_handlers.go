package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/llms/openai"
	"github.com/dengzii/weaveflow/memory"

	"github.com/gin-gonic/gin"
	"github.com/tmc/langchaingo/llms"
)

type graphRuntimeSettings struct {
	Environment        map[string]string        `json:"environment"`
	EnvironmentPresets []graphEnvironmentPreset `json:"environment_presets"`
	Model              graphModelSettings       `json:"model"`
	Models             []graphModelSettings     `json:"models"`
	Memory             graphMemorySettings      `json:"memory"`
}

type graphEnvironmentPreset struct {
	Key          string `json:"key"`
	DefaultValue string `json:"default_value"`
	Type         string `json:"type"`
}

type graphModelSettings struct {
	ID               string `json:"id,omitempty"`
	Enabled          bool   `json:"enabled"`
	Provider         string `json:"provider"`
	Model            string `json:"model,omitempty"`
	BaseURL          string `json:"base_url,omitempty"`
	APIKeyConfigured bool   `json:"api_key_configured"`
	APIKey           string `json:"-"`
}

type graphMemorySettings struct {
	Enabled   bool   `json:"enabled"`
	Directory string `json:"directory,omitempty"`
}

type graphRuntimeSettingsRequest struct {
	Environment map[string]string           `json:"environment"`
	Model       *graphModelSettingsRequest  `json:"model"`
	Models      []graphModelSettingsRequest `json:"models"`
	Memory      *graphMemorySettingsRequest `json:"memory"`
}

type graphModelSettingsRequest struct {
	ID       string `json:"id"`
	Enabled  *bool  `json:"enabled"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key"`
}

type graphMemorySettingsRequest struct {
	Enabled   *bool  `json:"enabled"`
	Directory string `json:"directory"`
}

const maxGraphSettingsBodyBytes int64 = 1 << 20

func (s *Server) handleGraphSettings(c *gin.Context) {
	writeData(c, http.StatusOK, s.graphSettingsSnapshot())
}

func (s *Server) handleSetGraphSettings(c *gin.Context) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()

	req, err := bindGraphSettingsRequest(c)
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}

	previous := s.graphSettingsSnapshot()
	next := previous
	apiKey, apiKeyProvided, err := applyGraphSettingsRequest(&next, req)
	if err != nil {
		writeError(c, http.StatusBadRequest, err)
		return
	}
	if !apiKeyProvided {
		apiKey = firstNonEmpty(firstGraphModelAPIKey(next), next.Environment["OPENAI_API_KEY"], os.Getenv("OPENAI_API_KEY"))
	}
	markGraphModelAPIKeys(&next, apiKey)
	if next.Memory.Enabled && strings.TrimSpace(next.Memory.Directory) == "" {
		next.Memory.Directory = defaultMemoryDirectory(s.baseDir)
	}

	ctx, err := s.buildRuntimeContext(next, apiKey)
	if err != nil {
		writeError(c, http.StatusBadRequest, err)
		return
	}
	rollbackEnvironment, err := applyGraphSettingsEnvironment(previous, next, apiKey, apiKeyProvided)
	if err != nil {
		writeError(c, http.StatusBadRequest, err)
		return
	}
	if err := persistGraphRuntimeSettings(s.baseDir, next); err != nil {
		if rollbackErr := rollbackEnvironment(); rollbackErr != nil {
			err = fmt.Errorf("%w; restore environment: %v", err, rollbackErr)
		}
		writeError(c, http.StatusInternalServerError, err)
		return
	}

	s.mu.Lock()
	s.settings = sanitizedGraphSettings(next)
	s.baseCtx = ctx
	s.mu.Unlock()

	writeData(c, http.StatusOK, s.graphSettingsSnapshot())
}

func bindGraphSettingsRequest(c *gin.Context) (graphRuntimeSettingsRequest, error) {
	body, err := readRequestBody(c.Request.Body, maxGraphSettingsBodyBytes)
	if err != nil {
		return graphRuntimeSettingsRequest{}, err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return graphRuntimeSettingsRequest{}, fmt.Errorf("graph settings body is required")
	}
	var req graphRuntimeSettingsRequest
	if err := decodeStrictJSON(body, &req); err != nil {
		return graphRuntimeSettingsRequest{}, fmt.Errorf("invalid JSON body: %w", err)
	}
	return req, nil
}

func (s *Server) graphSettingsSnapshot() graphRuntimeSettings {
	if s == nil {
		return graphRuntimeSettingsFromContext(context.Background(), "")
	}
	s.mu.RLock()
	settings := sanitizedGraphSettings(s.settings)
	s.mu.RUnlock()
	settings.EnvironmentPresets = graphEnvironmentPresets()
	markGraphModelAPIKeys(&settings, os.Getenv("OPENAI_API_KEY"))
	return settings
}

func graphRuntimeSettingsFromContext(ctx context.Context, baseDir string) graphRuntimeSettings {
	settings := graphRuntimeSettings{
		Environment:        currentGraphEnvironment(),
		EnvironmentPresets: graphEnvironmentPresets(),
		Memory: graphMemorySettings{
			Enabled:   core.MemoryFromContext(ctx) != nil,
			Directory: defaultMemoryDirectory(baseDir),
		},
	}
	settings.Models = graphModelSettingsFromContext(ctx)
	settings.Model = defaultGraphModelSettings(settings.Models)
	return sanitizedGraphSettings(settings)
}

func graphModelSettingsFromContext(ctx context.Context) []graphModelSettings {
	models := core.ModelsFromContext(ctx)
	if len(models) == 0 && core.ModelFromContext(ctx) == nil && strings.TrimSpace(os.Getenv("OPENAI_MODEL")) == "" && strings.TrimSpace(os.Getenv("OPENAI_BASE_URL")) == "" {
		return nil
	}
	if len(models) == 0 && core.ModelFromContext(ctx) != nil {
		models = map[string]llms.Model{core.DefaultModelID: core.ModelFromContext(ctx)}
	}
	ids := make([]string, 0, len(models))
	for id := range models {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		ids = append(ids, core.DefaultModelID)
	}
	sortModelIDs(ids)
	out := make([]graphModelSettings, 0, len(ids))
	for _, id := range ids {
		model := graphModelSettings{
			ID:               strings.TrimSpace(id),
			Enabled:          models[id] != nil,
			Provider:         "openai",
			APIKeyConfigured: strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != "",
		}
		if model.ID == core.DefaultModelID {
			model.Model = strings.TrimSpace(os.Getenv("OPENAI_MODEL"))
			model.BaseURL = strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
		}
		out = append(out, model)
	}
	return out
}

func (s *Server) buildRuntimeContext(settings graphRuntimeSettings, apiKey string) (context.Context, error) {
	ctx := context.Background()
	if tools := s.currentToolSet(); len(tools) > 0 {
		ctx = core.WithTools(ctx, tools)
	}
	if settings.Memory.Enabled {
		dir := strings.TrimSpace(settings.Memory.Directory)
		if dir == "" {
			dir = defaultMemoryDirectory(s.baseDir)
		}
		repo := memory.NewFileMemoryRepository(dir)
		ctx = core.WithMemory(ctx, memory.New(&memory.Options{Repository: repo, Retriever: memory.NewBM25Retriever(repo, nil)}))
	}
	models := map[string]llms.Model{}
	for _, modelSettings := range enabledGraphModels(settings) {
		modelAPIKey := firstNonEmpty(modelSettings.APIKey, apiKey, os.Getenv("OPENAI_API_KEY"))
		if modelAPIKey == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY is required when model %q is enabled", modelSettings.ID)
		}
		model, err := openai.New(
			openai.WithToken(modelAPIKey),
			openai.WithModel(modelSettings.Model),
			openai.WithBaseURL(modelSettings.BaseURL),
		)
		if err != nil {
			return nil, fmt.Errorf("configure model %q: %w", modelSettings.ID, err)
		}
		models[modelSettings.ID] = model
	}
	if len(models) > 0 {
		ctx = core.WithModels(ctx, models)
	}
	return ctx, nil
}

func enabledGraphModels(settings graphRuntimeSettings) []graphModelSettings {
	models := sanitizedGraphModelList(settings.Models, settings.Model)
	out := make([]graphModelSettings, 0, len(models))
	for _, model := range models {
		if model.Enabled {
			out = append(out, model)
		}
	}
	return out
}

func (s *Server) currentToolSet() map[string]core.Tool {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneTools(core.ToolsFromContext(s.baseCtx))
}

func cloneTools(input map[string]core.Tool) map[string]core.Tool {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]core.Tool, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
