package server

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

func currentGraphEnvironment() map[string]string {
	environment := map[string]string{}
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
			environment[key] = value
		}
	}
	return environment
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

func applyGraphSettingsEnvironment(
	previous graphRuntimeSettings,
	next graphRuntimeSettings,
	apiKey string,
	apiKeyProvided bool,
) (func() error, error) {
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
	for _, key := range keys {
		value, exists := os.LookupEnv(key)
		originals[key] = originalEnvironmentValue{value: value, exists: exists}
	}

	// Persistence happens after process state changes, so return a rollback closure
	// that restores the exact prior environment if the durable write fails.
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

func isSecretEnvironmentName(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	return strings.Contains(upper, "KEY") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD")
}
