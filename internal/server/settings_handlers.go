package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/llms"
	"github.com/dengzii/weaveflow/llms/openai"
)

type graphRuntimeSettings struct {
	Environment        map[string]string        `json:"environment"`
	EnvironmentPresets []graphEnvironmentPreset `json:"environment_presets"`
	Models             []graphModelSettings     `json:"models"`
	ToolPermissions    []string                 `json:"tool_permissions"`
	ToolApprovals      map[string]bool          `json:"tool_approvals"`
}

type graphEnvironmentPreset struct {
	Key          string `json:"key"`
	DefaultValue string `json:"default_value"`
	Type         string `json:"type"`
}

type graphModelSettings struct {
	ID               string            `json:"id,omitempty"`
	Enabled          bool              `json:"enabled"`
	Provider         string            `json:"provider"`
	APIFormat        string            `json:"api_format"`
	Model            string            `json:"model,omitempty"`
	BaseURL          string            `json:"base_url,omitempty"`
	ExtraBody        map[string]any    `json:"extra_body,omitempty"`
	APIKeyConfigured bool              `json:"api_key_configured"`
	APIKey           string            `json:"-"`
	Pricing          llms.ModelPricing `json:"pricing,omitempty"`
}

type graphRuntimeSettingsRequest struct {
	Environment     map[string]string           `json:"environment"`
	Models          []graphModelSettingsRequest `json:"models"`
	ToolPermissions []string                    `json:"tool_permissions"`
	ToolApprovals   map[string]bool             `json:"tool_approvals"`
}

type graphModelSettingsRequest struct {
	ID        string            `json:"id"`
	Enabled   *bool             `json:"enabled"`
	Provider  string            `json:"provider"`
	APIFormat string            `json:"api_format"`
	Model     string            `json:"model"`
	BaseURL   string            `json:"base_url"`
	ExtraBody map[string]any    `json:"extra_body"`
	APIKey    string            `json:"api_key"`
	Pricing   llms.ModelPricing `json:"pricing"`
}

func graphSettingsResponse(settings graphRuntimeSettings) graphRuntimeSettings {
	settings = sanitizedGraphSettings(settings)
	settings.EnvironmentPresets = graphEnvironmentPresets()
	return settings
}

func (s *Server) runtimeSettingsForGraph(graphID string) (graphRuntimeSettings, error) {
	if s == nil || s.runtime == nil {
		return graphRuntimeSettingsFromContext(context.Background()), nil
	}
	current := s.runtime.currentSession()
	if current.runner != nil && effectiveRunnerGraphID(current.runner) == strings.TrimSpace(graphID) {
		return normalizedGraphSettings(current.settings), nil
	}
	session, err := s.latestGraphSession(graphID)
	if err == nil {
		settings, found, loadErr := loadGraphRuntimeSettings(session.baseDir)
		if loadErr != nil {
			return graphRuntimeSettings{}, loadErr
		}
		if !found {
			return graphRuntimeSettings{}, fmt.Errorf("graph session %q settings are missing", session.manifest.GraphSessionID)
		}
		return settings, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return graphRuntimeSettings{}, err
	}
	settings, _ := s.runtime.defaults()
	return settings, nil
}

func graphRuntimeSettingsFromContext(ctx context.Context) graphRuntimeSettings {
	environment := currentGraphEnvironment()
	for key, value := range core.EnvironmentFromContext(ctx) {
		environment[key] = value
	}
	settings := graphRuntimeSettings{
		Environment:        environment,
		EnvironmentPresets: graphEnvironmentPresets(),
	}
	settings.Models = graphModelSettingsFromContext(ctx)
	if permissions, configured := core.ToolPermissionsFromContext(ctx); configured {
		settings.ToolPermissions = permissions
	} else {
		settings.ToolPermissions = defaultToolPermissions()
	}
	settings.ToolApprovals = map[string]bool{}
	return normalizedGraphSettings(settings)
}

func graphModelSettingsFromContext(ctx context.Context) []graphModelSettings {
	models := core.ModelsFromContext(ctx)
	modelConfigs := core.ModelConfigsFromContext(ctx)
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
		config := modelConfigs[id]
		model := graphModelSettings{
			ID:               strings.TrimSpace(id),
			Enabled:          models[id] != nil,
			Provider:         firstNonEmpty(config.Provider, string(openai.ProviderOpenAI)),
			APIFormat:        firstNonEmpty(config.APIFormat, string(openai.APIFormatChatCompletions)),
			Model:            config.Model,
			BaseURL:          config.BaseURL,
			ExtraBody:        cloneGraphModelExtraBody(config.ExtraBody),
			APIKeyConfigured: strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != "",
			Pricing:          config.Pricing,
		}
		if model.ID == core.DefaultModelID {
			model.Model = firstNonEmpty(model.Model, os.Getenv("OPENAI_MODEL"))
			model.BaseURL = firstNonEmpty(model.BaseURL, os.Getenv("OPENAI_BASE_URL"))
		}
		out = append(out, model)
	}
	return out
}

func (s *Server) buildRuntimeContext(settings graphRuntimeSettings, apiKey string) (context.Context, error) {
	ctx := core.WithEnvironment(context.Background(), settings.Environment)
	if tools := s.currentToolSet(); len(tools) > 0 {
		ctx = core.WithTools(ctx, tools)
	}
	models := map[string]llms.Model{}
	modelConfigs := map[string]core.ModelConfig{}
	for _, modelSettings := range enabledGraphModels(settings) {
		modelAPIKey := firstNonEmpty(modelSettings.APIKey, apiKey, os.Getenv("OPENAI_API_KEY"))
		if modelAPIKey == "" {
			return nil, fmt.Errorf("model %q requires OPENAI_API_KEY when enabled", modelSettings.ID)
		}
		model, err := openai.New(
			openai.WithToken(modelAPIKey),
			openai.WithModel(modelSettings.Model),
			openai.WithBaseURL(modelSettings.BaseURL),
			openai.WithProvider(openai.Provider(modelSettings.Provider)),
			openai.WithAPIFormat(openai.APIFormat(modelSettings.APIFormat)),
			openai.WithExtraBody(modelSettings.ExtraBody),
		)
		if err != nil {
			return nil, fmt.Errorf("configure model %q: %w", modelSettings.ID, err)
		}
		models[modelSettings.ID] = model
		modelConfigs[modelSettings.ID] = core.ModelConfig{
			ID:        modelSettings.ID,
			Provider:  modelSettings.Provider,
			APIFormat: modelSettings.APIFormat,
			Model:     modelSettings.Model,
			BaseURL:   modelSettings.BaseURL,
			ExtraBody: modelSettings.ExtraBody,
			APIKey:    modelAPIKey,
			Pricing:   modelSettings.Pricing,
		}
	}
	if len(models) > 0 {
		ctx = core.WithModels(ctx, models)
	}
	ctx = core.WithModelConfigs(ctx, modelConfigs)
	ctx = core.WithToolPermissions(ctx, settings.ToolPermissions...)
	ctx = core.WithToolApprover(ctx, core.ToolApproverFunc(func(_ context.Context, request core.ToolApprovalRequest) (core.ToolApprovalDecision, error) {
		name := ""
		if request.ToolCall.FunctionCall != nil {
			name = strings.TrimSpace(request.ToolCall.FunctionCall.Name)
		}
		approved, configured := settings.ToolApprovals[strings.ToLower(name)]
		if !configured {
			return core.ToolApprovalDecision{Approved: false, Reason: "no explicit approval decision configured"}, nil
		}
		return core.ToolApprovalDecision{
			ApprovalID: "runtime-settings:" + strings.ToLower(name),
			Approved:   approved,
			Actor:      "runtime-settings",
			Reason:     "graph runtime settings decision",
		}, nil
	}))
	return applyRuntimeContextDecorators(ctx, s.cfg.RuntimeContextDecorators), nil
}

func defaultToolPermissions() []string {
	return []string{"filesystem.read", "network.http", "network.search"}
}

func enabledGraphModels(settings graphRuntimeSettings) []graphModelSettings {
	models := sanitizedGraphModelList(settings.Models)
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
	return cloneTools(core.ToolsFromContext(s.runtime.runtimeContext()))
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
