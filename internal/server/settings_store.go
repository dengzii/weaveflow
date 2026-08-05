package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	graphRuntimeSettingsFileName = "runtime-settings.json"
	graphRuntimeSettingsVersion  = 1
)

type graphRuntimeSettingsFile struct {
	Version     int                      `json:"version"`
	Environment map[string]string        `json:"environment"`
	Models      []graphModelSettingsFile `json:"models"`
	Memory      graphMemorySettings      `json:"memory"`
}

type graphModelSettingsFile struct {
	ID       string `json:"id"`
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider"`
	Model    string `json:"model,omitempty"`
	BaseURL  string `json:"base_url,omitempty"`
	APIKey   string `json:"api_key,omitempty"`
}

func loadGraphRuntimeSettings(baseDir string) (graphRuntimeSettings, bool, error) {
	path := graphRuntimeSettingsPath(baseDir)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return graphRuntimeSettings{}, false, nil
	}
	if err != nil {
		return graphRuntimeSettings{}, false, fmt.Errorf("read graph runtime settings: %w", err)
	}
	var stored graphRuntimeSettingsFile
	if err := decodeStrictJSON(data, &stored); err != nil {
		return graphRuntimeSettings{}, false, fmt.Errorf("decode graph runtime settings: %w", err)
	}
	if stored.Version != graphRuntimeSettingsVersion {
		return graphRuntimeSettings{}, false, fmt.Errorf("unsupported graph runtime settings version %d", stored.Version)
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
		if strings.TrimSpace(model.Provider) != "openai" {
			return graphRuntimeSettings{}, false, fmt.Errorf("graph runtime settings model %q provider must be openai", modelID)
		}
	}
	settings := graphRuntimeSettings{
		Environment: stored.Environment,
		Memory:      stored.Memory,
		Models:      make([]graphModelSettings, 0, len(stored.Models)),
	}
	for _, model := range stored.Models {
		settings.Models = append(settings.Models, graphModelSettings{
			ID:               model.ID,
			Enabled:          model.Enabled,
			Provider:         model.Provider,
			Model:            model.Model,
			BaseURL:          model.BaseURL,
			APIKeyConfigured: strings.TrimSpace(model.APIKey) != "",
			APIKey:           strings.TrimSpace(model.APIKey),
		})
	}
	markGraphModelAPIKeys(&settings, firstGraphModelAPIKey(settings))
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
		Version:     graphRuntimeSettingsVersion,
		Environment: settings.Environment,
		Models:      make([]graphModelSettingsFile, 0, len(settings.Models)),
		Memory:      settings.Memory,
	}
	for _, model := range settings.Models {
		stored.Models = append(stored.Models, graphModelSettingsFile{
			ID:       model.ID,
			Enabled:  model.Enabled,
			Provider: model.Provider,
			Model:    model.Model,
			BaseURL:  model.BaseURL,
			APIKey:   strings.TrimSpace(model.APIKey),
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
	return filepath.Join(baseDir, graphRuntimeSettingsFileName)
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
