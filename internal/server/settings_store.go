package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/llms"
	"github.com/dengzii/weaveflow/llms/openai"
)

const (
	graphRuntimeSettingsFileName = "runtime-settings.json"
	graphRuntimeSettingsVersion  = 4
)

type graphRuntimeSettingsFile struct {
	Version            int                      `json:"version"`
	Environment        map[string]string        `json:"environment"`
	EnvironmentSecrets map[string]dsl.SecretRef `json:"environment_secrets"`
	Models             []graphModelSettingsFile `json:"models"`
	ToolPermissions    []string                 `json:"tool_permissions"`
	ToolApprovals      map[string]bool          `json:"tool_approvals"`
}

type graphModelSettingsFile struct {
	ID        string            `json:"id"`
	Enabled   bool              `json:"enabled"`
	Provider  string            `json:"provider"`
	APIFormat string            `json:"api_format,omitempty"`
	Model     string            `json:"model,omitempty"`
	BaseURL   string            `json:"base_url,omitempty"`
	ExtraBody map[string]any    `json:"extra_body,omitempty"`
	Pricing   llms.ModelPricing `json:"pricing,omitempty"`
}

func loadGraphRuntimeSettings(baseDir string) (graphRuntimeSettings, bool, error) {
	path := graphRuntimeSettingsPath(baseDir)
	root, err := os.OpenRoot(filepath.Dir(path))
	if os.IsNotExist(err) {
		return graphRuntimeSettings{}, false, nil
	}
	if err != nil {
		return graphRuntimeSettings{}, false, fmt.Errorf("open graph runtime settings directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	data, err := root.ReadFile(filepath.Base(path))
	if os.IsNotExist(err) {
		return graphRuntimeSettings{}, false, nil
	}
	if err != nil {
		return graphRuntimeSettings{}, false, fmt.Errorf("read graph runtime settings: %w", err)
	}
	var header struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return graphRuntimeSettings{}, false, fmt.Errorf("decode graph runtime settings version: %w", err)
	}
	if header.Version != graphRuntimeSettingsVersion {
		return graphRuntimeSettings{}, false, fmt.Errorf("unsupported graph runtime settings version %d", header.Version)
	}
	var stored graphRuntimeSettingsFile
	if err := decodeStrictJSON(data, &stored); err != nil {
		return graphRuntimeSettings{}, false, fmt.Errorf("decode graph runtime settings: %w", err)
	}
	for name := range stored.Environment {
		if err := validateEnvironmentName(strings.TrimSpace(name)); err != nil {
			return graphRuntimeSettings{}, false, fmt.Errorf("graph runtime settings environment: %w", err)
		}
		if isSecretEnvironmentName(name) {
			return graphRuntimeSettings{}, false, fmt.Errorf("graph runtime settings environment variable %q must use environment_secrets", name)
		}
	}
	for name, ref := range stored.EnvironmentSecrets {
		if err := validateEnvironmentName(strings.TrimSpace(name)); err != nil {
			return graphRuntimeSettings{}, false, fmt.Errorf("graph runtime settings environment secret: %w", err)
		}
		if !isSecretEnvironmentName(name) {
			return graphRuntimeSettings{}, false, fmt.Errorf("graph runtime settings environment secret name %q must be secret-like", name)
		}
		if _, err := normalizeSecretRef(ref); err != nil {
			return graphRuntimeSettings{}, false, fmt.Errorf("graph runtime settings environment secret %q: %w", name, err)
		}
	}
	seenModelIDs := make(map[string]struct{}, len(stored.Models))
	for index, model := range stored.Models {
		modelID := strings.TrimSpace(model.ID)
		if modelID == "" {
			return graphRuntimeSettings{}, false, fmt.Errorf("graph runtime settings model %d id is required", index)
		}
		if _, exists := seenModelIDs[modelID]; exists {
			return graphRuntimeSettings{}, false, fmt.Errorf("graph runtime settings model id %q is duplicated", modelID)
		}
		seenModelIDs[modelID] = struct{}{}
		if !openai.IsSupportedProvider(openai.Provider(strings.TrimSpace(model.Provider))) {
			return graphRuntimeSettings{}, false, fmt.Errorf("graph runtime settings model %q has unsupported provider %q", modelID, model.Provider)
		}
		apiFormat := firstNonEmpty(model.APIFormat, string(openai.APIFormatChatCompletions))
		if !openai.IsSupportedAPIFormat(openai.APIFormat(apiFormat)) {
			return graphRuntimeSettings{}, false, fmt.Errorf("graph runtime settings model %q has unsupported API format %q", modelID, apiFormat)
		}
		pricing, err := normalizeModelPricing(model.Pricing)
		if err != nil {
			return graphRuntimeSettings{}, false, fmt.Errorf("graph runtime settings model %q pricing: %w", modelID, err)
		}
		stored.Models[index].Pricing = pricing
	}
	settings := graphRuntimeSettings{
		Environment:        stored.Environment,
		EnvironmentSecrets: stored.EnvironmentSecrets,
		Models:             make([]graphModelSettings, 0, len(stored.Models)),
		ToolPermissions:    stored.ToolPermissions,
		ToolApprovals:      stored.ToolApprovals,
	}
	for _, model := range stored.Models {
		settings.Models = append(settings.Models, graphModelSettings{
			ID:        model.ID,
			Enabled:   model.Enabled,
			Provider:  model.Provider,
			APIFormat: model.APIFormat,
			Model:     model.Model,
			BaseURL:   model.BaseURL,
			ExtraBody: cloneGraphModelExtraBody(model.ExtraBody),
			Pricing:   model.Pricing,
		})
	}
	return normalizedGraphSettings(settings), true, nil
}

func persistGraphRuntimeSettings(baseDir string, settings graphRuntimeSettings) error {
	data, err := encodeGraphRuntimeSettings(settings)
	if err != nil {
		return err
	}
	if err := writeGraphRuntimeSettingsFile(graphRuntimeSettingsPath(baseDir), data); err != nil {
		return fmt.Errorf("write graph runtime settings: %w", err)
	}
	return nil
}

func encodeGraphRuntimeSettings(settings graphRuntimeSettings) ([]byte, error) {
	settings = normalizedGraphSettings(settings)
	stored := graphRuntimeSettingsFile{
		Version:            graphRuntimeSettingsVersion,
		Environment:        settings.Environment,
		EnvironmentSecrets: settings.EnvironmentSecrets,
		Models:             make([]graphModelSettingsFile, 0, len(settings.Models)),
		ToolPermissions:    settings.ToolPermissions,
		ToolApprovals:      settings.ToolApprovals,
	}
	for _, model := range settings.Models {
		stored.Models = append(stored.Models, graphModelSettingsFile{
			ID:        model.ID,
			Enabled:   model.Enabled,
			Provider:  model.Provider,
			APIFormat: model.APIFormat,
			Model:     model.Model,
			BaseURL:   model.BaseURL,
			ExtraBody: cloneGraphModelExtraBody(model.ExtraBody),
			Pricing:   model.Pricing,
		})
	}
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode graph runtime settings: %w", err)
	}
	data = append(data, '\n')
	return data, nil
}

func graphRuntimeSettingsPath(baseDir string) string {
	absolute, err := filepath.Abs(baseDir)
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Clean("/"+absolute), graphRuntimeSettingsFileName)
}

func writeGraphRuntimeSettingsFile(path string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".runtime-settings-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
