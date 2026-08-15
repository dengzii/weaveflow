package server

import (
	"fmt"
	"os"
	"strings"

	"github.com/dengzii/weaveflow/dsl"
)

func currentGraphEnvironment() map[string]string {
	environment := map[string]string{}
	keys := []string{
		"OPENAI_MODEL",
		"OPENAI_BASE_URL",
		"OPENAI_ORGANIZATION",
	}
	for _, preset := range graphEnvironmentPresets() {
		if !preset.Secret {
			keys = append(keys, preset.Key)
		}
	}
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			environment[key] = value
		}
	}
	return environment
}

func currentGraphEnvironmentSecrets() map[string]dsl.SecretRef {
	secrets := map[string]dsl.SecretRef{}
	for _, preset := range graphEnvironmentPresets() {
		if preset.Secret && strings.TrimSpace(os.Getenv(preset.Key)) != "" {
			secrets[preset.Key] = dsl.SecretRef{Source: "env", Ref: preset.Key}
		}
	}
	return secrets
}

func graphEnvironmentPresets() []graphEnvironmentPreset {
	return []graphEnvironmentPreset{
		{Key: "TAVILY_API_KEY", Type: "string", Secret: true},
		{Key: "WEAVEFLOW_TOOL_WORKDIR", Type: "string"},
		{Key: "WEAVEFLOW_TOOL_SKIP_WORKSPACE_CHECK", DefaultValue: "false", Type: "boolean"},
		{Key: "WEAVEFLOW_BASH_TIMEOUT", DefaultValue: "120000", Type: "integer"},
		{Key: "WEAVEFLOW_BASH_ALLOWLIST", Type: "string"},
		{Key: "GIT_BASH", Type: "string"},
		{Key: "MSYS2_BASH", Type: "string"},
		{Key: "MINGW_BASH", Type: "string"},
	}
}

func validateEnvironmentName(key string) error {
	if key == "" || strings.ContainsAny(key, "=\x00") {
		return fmt.Errorf("invalid environment variable name %q", key)
	}
	return nil
}

func isSecretEnvironmentName(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	return strings.Contains(upper, "KEY") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD")
}
