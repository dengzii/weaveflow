package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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

func currentGraphEnvironment() map[string]string {
	env := map[string]string{}
	keys := []string{
		"OPENAI_MODEL",
		"OPENAI_BASE_URL",
		"OPENAI_ORGANIZATION",
	}
	for _, preset := range graphEnvironmentPresets() {
		keys = append(keys, preset.Key)
	}
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			env[key] = value
		}
	}
	return env
}

func graphEnvironmentPresets() []graphEnvironmentPreset {
	return []graphEnvironmentPreset{
		{Key: "WEAVEFLOW_TOOL_WORKDIR", Type: "string"},
		{Key: "WEAVEFLOW_TOOL_SKIP_WORKSPACE_CHECK", DefaultValue: "false", Type: "boolean"},
		{Key: "WEAVEFLOW_BASH_TIMEOUT", DefaultValue: "120000", Type: "integer"},
		{Key: "WEAVEFLOW_BASH_ALLOWLIST", Type: "string"},
		{Key: "GIT_BASH", Type: "string"},
		{Key: "MSYS2_BASH", Type: "string"},
		{Key: "MINGW_BASH", Type: "string"},
	}
}

func applyGraphSettingsRequest(settings *graphRuntimeSettings, req graphRuntimeSettingsRequest) (string, bool, error) {
	if req.Environment != nil {
		settings.Environment = map[string]string{}
		for key, value := range req.Environment {
			name := strings.TrimSpace(key)
			if err := validateEnvironmentName(name); err != nil {
				return "", false, err
			}
			settings.Environment[name] = strings.TrimSpace(value)
		}
	} else if settings.Environment == nil {
		settings.Environment = map[string]string{}
	}

	apiKey := ""
	apiKeyProvided := false
	if req.Models != nil {
		models, modelAPIKey, modelAPIKeyProvided, err := graphModelSettingsListFromRequest(settings.Models, req.Models)
		if err != nil {
			return "", false, err
		}
		settings.Models = models
		settings.Model = defaultGraphModelSettings(settings.Models)
		if modelAPIKeyProvided {
			apiKey = modelAPIKey
			apiKeyProvided = true
		}
	} else if req.Model != nil {
		model, modelAPIKey, modelAPIKeyProvided, err := graphModelSettingsFromRequest(settings.Model, *req.Model, core.DefaultModelID)
		if err != nil {
			return "", false, err
		}
		settings.Model = model
		settings.Models = []graphModelSettings{model}
		if modelAPIKeyProvided {
			apiKey = modelAPIKey
			apiKeyProvided = true
		}
	} else {
		applyEnvironmentModelDefaults(settings)
	}
	if value, ok := req.Environment["OPENAI_API_KEY"]; ok {
		apiKey = strings.TrimSpace(value)
		apiKeyProvided = true
	}
	syncGraphModelEnvironment(settings)

	if req.Memory != nil {
		if req.Memory.Enabled != nil {
			settings.Memory.Enabled = *req.Memory.Enabled
		}
		settings.Memory.Directory = strings.TrimSpace(req.Memory.Directory)
	}
	return apiKey, apiKeyProvided, nil
}

func graphModelSettingsListFromRequest(currentModels []graphModelSettings, reqModels []graphModelSettingsRequest) ([]graphModelSettings, string, bool, error) {
	models := make([]graphModelSettings, 0, len(reqModels))
	seen := map[string]struct{}{}
	currentByID := make(map[string]graphModelSettings, len(currentModels))
	for _, current := range currentModels {
		currentByID[strings.TrimSpace(current.ID)] = current
	}
	apiKey := ""
	apiKeyProvided := false
	for index, req := range reqModels {
		fallbackID := ""
		if index == 0 {
			fallbackID = core.DefaultModelID
		}
		current := currentByID[strings.TrimSpace(req.ID)]
		if current.ID == "" && index < len(currentModels) {
			current = currentModels[index]
		}
		model, modelAPIKey, modelAPIKeyProvided, err := graphModelSettingsFromRequest(current, req, fallbackID)
		if err != nil {
			return nil, "", false, err
		}
		if _, ok := seen[model.ID]; ok {
			return nil, "", false, fmt.Errorf("duplicate model id %q", model.ID)
		}
		seen[model.ID] = struct{}{}
		if modelAPIKeyProvided && !apiKeyProvided {
			apiKey = modelAPIKey
			apiKeyProvided = true
		}
		models = append(models, model)
	}
	return models, apiKey, apiKeyProvided, nil
}

func graphModelSettingsFromRequest(current graphModelSettings, req graphModelSettingsRequest, fallbackID string) (graphModelSettings, string, bool, error) {
	model := current
	model.ID = firstNonEmpty(req.ID, model.ID, fallbackID)
	model.ID = strings.TrimSpace(model.ID)
	if model.ID == "" {
		return graphModelSettings{}, "", false, fmt.Errorf("model id is required")
	}
	if req.Enabled != nil {
		model.Enabled = *req.Enabled
	} else if current.ID == "" {
		model.Enabled = true
	}
	model.Provider = firstNonEmpty(req.Provider, model.Provider, "openai")
	if strings.TrimSpace(model.Provider) != "openai" {
		return graphModelSettings{}, "", false, fmt.Errorf("unsupported model provider %q", model.Provider)
	}
	model.Model = strings.TrimSpace(req.Model)
	model.BaseURL = strings.TrimSpace(req.BaseURL)
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey != "" {
		model.APIKey = apiKey
		model.APIKeyConfigured = true
	}
	return sanitizeGraphModelSettings(model, model.ID), apiKey, apiKey != "", nil
}

func applyEnvironmentModelDefaults(settings *graphRuntimeSettings) {
	if settings == nil || len(settings.Models) == 0 {
		return
	}
	for index := range settings.Models {
		if settings.Models[index].ID != core.DefaultModelID {
			continue
		}
		if value, ok := settings.Environment["OPENAI_MODEL"]; ok {
			settings.Models[index].Model = strings.TrimSpace(value)
		}
		if value, ok := settings.Environment["OPENAI_BASE_URL"]; ok {
			settings.Models[index].BaseURL = strings.TrimSpace(value)
		}
		settings.Model = settings.Models[index]
		return
	}
}

func syncGraphModelEnvironment(settings *graphRuntimeSettings) {
	if settings.Environment == nil {
		settings.Environment = map[string]string{}
	}
	settings.Model = defaultGraphModelSettings(settings.Models)
	settings.Environment["OPENAI_MODEL"] = settings.Model.Model
	settings.Environment["OPENAI_BASE_URL"] = settings.Model.BaseURL
}

func markGraphModelAPIKeys(settings *graphRuntimeSettings, apiKey string) {
	if settings == nil {
		return
	}
	configured := strings.TrimSpace(apiKey) != ""
	for index := range settings.Models {
		settings.Models[index].APIKeyConfigured = settings.Models[index].APIKeyConfigured || configured || strings.TrimSpace(settings.Models[index].APIKey) != ""
	}
	settings.Model = defaultGraphModelSettings(settings.Models)
	settings.Model.APIKeyConfigured = settings.Model.APIKeyConfigured || configured || strings.TrimSpace(settings.Model.APIKey) != ""
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

func applyGraphSettingsEnvironment(previous graphRuntimeSettings, next graphRuntimeSettings, apiKey string, apiKeyProvided bool) (func() error, error) {
	changes := make(map[string]string, len(previous.Environment)+len(next.Environment)+1)
	for key := range previous.Environment {
		if _, exists := next.Environment[key]; !exists {
			changes[key] = ""
		}
	}
	for key, value := range next.Environment {
		changes[key] = value
	}
	if apiKeyProvided {
		changes["OPENAI_API_KEY"] = apiKey
	}

	keys := make([]string, 0, len(changes))
	for key, value := range changes {
		if err := validateEnvironmentName(key); err != nil {
			return nil, err
		}
		if strings.ContainsRune(value, '\x00') {
			return nil, fmt.Errorf("environment variable %q contains a null byte", key)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	type originalEnvironmentValue struct {
		value  string
		exists bool
	}
	originals := make(map[string]originalEnvironmentValue, len(keys))
	restore := func() error {
		var firstErr error
		for _, key := range keys {
			original := originals[key]
			var err error
			if original.exists {
				err = os.Setenv(key, original.value)
			} else {
				err = os.Unsetenv(key)
			}
			if err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}
	for _, key := range keys {
		value, exists := os.LookupEnv(key)
		originals[key] = originalEnvironmentValue{value: value, exists: exists}
	}
	for _, key := range keys {
		if err := setOrUnsetEnv(key, changes[key]); err != nil {
			_ = restore()
			return nil, err
		}
	}
	return restore, nil
}

func setOrUnsetEnv(key string, value string) error {
	if err := validateEnvironmentName(key); err != nil {
		return err
	}
	if strings.TrimSpace(value) == "" {
		return os.Unsetenv(key)
	}
	return os.Setenv(key, value)
}

func validateEnvironmentName(key string) error {
	if key == "" || strings.ContainsAny(key, "=\x00") {
		return fmt.Errorf("invalid environment variable name %q", key)
	}
	return nil
}

func sanitizedGraphSettings(settings graphRuntimeSettings) graphRuntimeSettings {
	env := make(map[string]string, len(settings.Environment))
	for key, value := range settings.Environment {
		name := strings.TrimSpace(key)
		if name == "" || isSecretEnvironmentName(name) {
			continue
		}
		env[name] = strings.TrimSpace(value)
	}
	settings.Environment = env
	settings.Models = sanitizedGraphModelList(settings.Models, settings.Model)
	if settings.Models == nil {
		settings.Models = []graphModelSettings{}
	}
	settings.Model = defaultGraphModelSettings(settings.Models)
	settings.Memory.Directory = strings.TrimSpace(settings.Memory.Directory)
	return settings
}

func sanitizedGraphModelList(models []graphModelSettings, fallback graphModelSettings) []graphModelSettings {
	if len(models) == 0 {
		fallback = sanitizeGraphModelSettings(fallback, core.DefaultModelID)
		if fallback.Enabled || fallback.Model != "" || fallback.BaseURL != "" || fallback.APIKeyConfigured {
			return []graphModelSettings{fallback}
		}
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]graphModelSettings, 0, len(models))
	for index, model := range models {
		fallbackID := ""
		if index == 0 {
			fallbackID = core.DefaultModelID
		}
		model = sanitizeGraphModelSettings(model, fallbackID)
		if model.ID == "" {
			continue
		}
		if _, ok := seen[model.ID]; ok {
			continue
		}
		seen[model.ID] = struct{}{}
		out = append(out, model)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ID == core.DefaultModelID {
			return true
		}
		if out[j].ID == core.DefaultModelID {
			return false
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func sanitizeGraphModelSettings(model graphModelSettings, fallbackID string) graphModelSettings {
	model.ID = firstNonEmpty(model.ID, fallbackID)
	model.ID = strings.TrimSpace(model.ID)
	model.Provider = firstNonEmpty(model.Provider, "openai")
	model.Provider = strings.TrimSpace(model.Provider)
	model.Model = strings.TrimSpace(model.Model)
	model.BaseURL = strings.TrimSpace(model.BaseURL)
	model.APIKey = strings.TrimSpace(model.APIKey)
	return model
}

func firstGraphModelAPIKey(settings graphRuntimeSettings) string {
	for _, model := range settings.Models {
		if model.ID == core.DefaultModelID && strings.TrimSpace(model.APIKey) != "" {
			return strings.TrimSpace(model.APIKey)
		}
	}
	for _, model := range settings.Models {
		if strings.TrimSpace(model.APIKey) != "" {
			return strings.TrimSpace(model.APIKey)
		}
	}
	return ""
}

func defaultGraphModelSettings(models []graphModelSettings) graphModelSettings {
	if len(models) == 0 {
		return graphModelSettings{ID: core.DefaultModelID, Provider: "openai"}
	}
	for _, model := range models {
		if model.ID == core.DefaultModelID {
			return model
		}
	}
	return models[0]
}

func sortModelIDs(ids []string) {
	sort.SliceStable(ids, func(i, j int) bool {
		if ids[i] == core.DefaultModelID {
			return true
		}
		if ids[j] == core.DefaultModelID {
			return false
		}
		return ids[i] < ids[j]
	})
}

func isSecretEnvironmentName(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	return strings.Contains(upper, "KEY") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD")
}

func defaultMemoryDirectory(baseDir string) string {
	if strings.TrimSpace(baseDir) == "" {
		return ""
	}
	return filepath.Join(baseDir, "memory")
}
