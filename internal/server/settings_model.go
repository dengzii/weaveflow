package server

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/llms/openai"
)

func applyGraphSettingsRequest(settings *graphRuntimeSettings, req graphRuntimeSettingsRequest) (string, bool, error) {
	if req.Environment != nil {
		environment := map[string]string{}
		for key, value := range settings.Environment {
			if isSecretEnvironmentName(key) {
				environment[key] = value
			}
		}
		for key, value := range req.Environment {
			name := strings.TrimSpace(key)
			if err := validateEnvironmentName(name); err != nil {
				return "", false, err
			}
			environment[name] = strings.TrimSpace(value)
		}
		settings.Environment = environment
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

	return apiKey, apiKeyProvided, nil
}

func graphModelSettingsListFromRequest(
	currentModels []graphModelSettings,
	reqModels []graphModelSettingsRequest,
) ([]graphModelSettings, string, bool, error) {
	models := make([]graphModelSettings, 0, len(reqModels))
	seen := map[string]struct{}{}
	currentByID := make(map[string]graphModelSettings, len(currentModels))
	for _, current := range currentModels {
		currentByID[strings.TrimSpace(current.ID)] = current
	}
	apiKey := ""
	apiKeyProvided := false
	for _, req := range reqModels {
		current := currentByID[strings.TrimSpace(req.ID)]
		model, modelAPIKey, modelAPIKeyProvided, err := graphModelSettingsFromRequest(current, req)
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

func graphModelSettingsFromRequest(
	current graphModelSettings,
	req graphModelSettingsRequest,
) (graphModelSettings, string, bool, error) {
	modelID := strings.TrimSpace(req.ID)
	if modelID == "" {
		return graphModelSettings{}, "", false, fmt.Errorf("model id is required")
	}
	model := current
	model.ID = modelID
	if req.Enabled != nil {
		model.Enabled = *req.Enabled
	} else if current.ID == "" {
		model.Enabled = true
	}
	model.Provider = firstNonEmpty(req.Provider, model.Provider, "openai")
	model.Provider = strings.ToLower(strings.TrimSpace(model.Provider))
	if !openai.IsSupportedProvider(openai.Provider(model.Provider)) {
		return graphModelSettings{}, "", false, fmt.Errorf("unsupported model provider %q", model.Provider)
	}
	model.APIFormat = firstNonEmpty(req.APIFormat, model.APIFormat, string(openai.APIFormatChatCompletions))
	model.APIFormat = strings.ToLower(strings.TrimSpace(model.APIFormat))
	if !openai.IsSupportedAPIFormat(openai.APIFormat(model.APIFormat)) {
		return graphModelSettings{}, "", false, fmt.Errorf("unsupported model API format %q", model.APIFormat)
	}
	model.Model = strings.TrimSpace(req.Model)
	model.BaseURL = strings.TrimSpace(req.BaseURL)
	if req.ExtraBody != nil {
		model.ExtraBody = cloneGraphModelExtraBody(req.ExtraBody)
	}
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey != "" {
		model.APIKey = apiKey
		model.APIKeyConfigured = true
	}
	return sanitizeGraphModelSettings(model), apiKey, apiKey != "", nil
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
		return
	}
}

func syncGraphModelEnvironment(settings *graphRuntimeSettings) {
	if settings.Environment == nil {
		settings.Environment = map[string]string{}
	}
	defaultModel := defaultGraphModelSettings(settings.Models)
	settings.Environment["OPENAI_MODEL"] = defaultModel.Model
	settings.Environment["OPENAI_BASE_URL"] = defaultModel.BaseURL
}

func markGraphModelAPIKeys(settings *graphRuntimeSettings, apiKey string) {
	if settings == nil {
		return
	}
	configured := strings.TrimSpace(apiKey) != ""
	for index := range settings.Models {
		settings.Models[index].APIKeyConfigured = settings.Models[index].APIKeyConfigured || configured || strings.TrimSpace(settings.Models[index].APIKey) != ""
	}
}

func normalizedGraphSettings(settings graphRuntimeSettings) graphRuntimeSettings {
	environment := make(map[string]string, len(settings.Environment))
	for key, value := range settings.Environment {
		name := strings.TrimSpace(key)
		if name == "" {
			continue
		}
		environment[name] = strings.TrimSpace(value)
	}
	settings.Environment = environment
	settings.Models = sanitizedGraphModelList(settings.Models)
	if settings.Models == nil {
		settings.Models = []graphModelSettings{}
	}
	return settings
}

func sanitizedGraphSettings(settings graphRuntimeSettings) graphRuntimeSettings {
	settings = normalizedGraphSettings(settings)
	for key := range settings.Environment {
		if isSecretEnvironmentName(key) {
			delete(settings.Environment, key)
		}
	}
	return settings
}

func sanitizedGraphModelList(models []graphModelSettings) []graphModelSettings {
	if len(models) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]graphModelSettings, 0, len(models))
	for _, model := range models {
		model = sanitizeGraphModelSettings(model)
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

func sanitizeGraphModelSettings(model graphModelSettings) graphModelSettings {
	model.ID = strings.TrimSpace(model.ID)
	model.Provider = firstNonEmpty(model.Provider, "openai")
	model.Provider = strings.ToLower(strings.TrimSpace(model.Provider))
	model.APIFormat = firstNonEmpty(model.APIFormat, string(openai.APIFormatChatCompletions))
	model.APIFormat = strings.ToLower(strings.TrimSpace(model.APIFormat))
	model.Model = strings.TrimSpace(model.Model)
	model.BaseURL = strings.TrimSpace(model.BaseURL)
	model.ExtraBody = cloneGraphModelExtraBody(model.ExtraBody)
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
		return graphModelSettings{
			ID:        core.DefaultModelID,
			Provider:  string(openai.ProviderOpenAI),
			APIFormat: string(openai.APIFormatChatCompletions),
		}
	}
	for _, model := range models {
		if model.ID == core.DefaultModelID {
			return model
		}
	}
	return models[0]
}

func cloneGraphModelExtraBody(extraBody map[string]any) map[string]any {
	if len(extraBody) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(extraBody))
	for key, value := range extraBody {
		cloned[key] = value
	}
	return cloned
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
