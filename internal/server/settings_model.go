package server

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dengzii/weaveflow/core"
)

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

func graphModelSettingsFromRequest(
	current graphModelSettings,
	req graphModelSettingsRequest,
	fallbackID string,
) (graphModelSettings, string, bool, error) {
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

func sanitizedGraphSettings(settings graphRuntimeSettings) graphRuntimeSettings {
	environment := make(map[string]string, len(settings.Environment))
	for key, value := range settings.Environment {
		name := strings.TrimSpace(key)
		if name == "" || isSecretEnvironmentName(name) {
			continue
		}
		environment[name] = strings.TrimSpace(value)
	}
	settings.Environment = environment
	settings.Models = sanitizedGraphModelList(settings.Models)
	if settings.Models == nil {
		settings.Models = []graphModelSettings{}
	}
	settings.Memory.Directory = strings.TrimSpace(settings.Memory.Directory)
	return settings
}

func sanitizedGraphModelList(models []graphModelSettings) []graphModelSettings {
	if len(models) == 0 {
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

func defaultMemoryDirectory(baseDir string) string {
	if strings.TrimSpace(baseDir) == "" {
		return ""
	}
	return filepath.Join(baseDir, "memory")
}
