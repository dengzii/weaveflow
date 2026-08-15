package codex

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/llms/openai"
)

const (
	codexWorkspaceEnvironment        = "WEAVEFLOW_TOOL_WORKDIR"
	codexExecutableEnvironment       = "WEAVEFLOW_CODEX_EXECUTABLE"
	codexSandboxEnvironment          = "WEAVEFLOW_CODEX_SANDBOX"
	codexWorkspaceRootsEnvironment   = "WEAVEFLOW_CODEX_WORKSPACE_ROOTS"
	codexTimeoutSecondsEnvironment   = "WEAVEFLOW_CODEX_TIMEOUT_SECONDS"
	codexMaxStdoutBytesEnvironment   = "WEAVEFLOW_CODEX_MAX_STDOUT_BYTES"
	codexMaxStderrBytesEnvironment   = "WEAVEFLOW_CODEX_MAX_STDERR_BYTES"
	codexMaxConcurrencyEnvironment   = "WEAVEFLOW_CODEX_MAX_CONCURRENCY"
	codexEnvironmentNamesEnvironment = "WEAVEFLOW_CODEX_ENVIRONMENT_NAMES"
	defaultTimeoutSeconds            = 900
	defaultMaxStdoutBytes            = 8 * 1024 * 1024
	defaultMaxStderrBytes            = 1024 * 1024
	defaultMaxConcurrency            = 1
)

type Sandbox string

const (
	SandboxReadOnly       Sandbox = "read_only"
	SandboxWorkspaceWrite Sandbox = "workspace_write"
)

type RunnerConfig struct {
	Executable            string   `json:"executable,omitempty"`
	Sandbox               Sandbox  `json:"sandbox,omitempty"`
	AllowedWorkspaceRoots []string `json:"allowed_workspace_roots,omitempty"`
	TimeoutSeconds        int      `json:"timeout_seconds,omitempty"`
	MaxStdoutBytes        int64    `json:"max_stdout_bytes,omitempty"`
	MaxStderrBytes        int64    `json:"max_stderr_bytes,omitempty"`
	MaxConcurrency        int      `json:"max_concurrency,omitempty"`
	EnvironmentNames      []string `json:"environment_names,omitempty"`
}

type resolvedCodexRunnerConfig struct {
	RunnerConfig
	allowedWorkspaceRoots []string
}

type resolvedCodexRunConfig struct {
	resolvedCodexRunnerConfig
	executablePath string
	workspacePath  string
	modelID        string
	model          string
	baseURL        string
	environment    []string
	secretValues   []string
}

func DefaultRunnerConfig() RunnerConfig {
	return RunnerConfig{
		Executable:            "codex",
		Sandbox:               SandboxWorkspaceWrite,
		AllowedWorkspaceRoots: []string{"."},
		TimeoutSeconds:        defaultTimeoutSeconds,
		MaxStdoutBytes:        defaultMaxStdoutBytes,
		MaxStderrBytes:        defaultMaxStderrBytes,
		MaxConcurrency:        defaultMaxConcurrency,
	}
}

func RunnerConfigFromEnvironment() (RunnerConfig, error) {
	config := DefaultRunnerConfig()
	if value, ok := os.LookupEnv(codexExecutableEnvironment); ok {
		config.Executable = strings.TrimSpace(value)
	}
	if value, ok := os.LookupEnv(codexSandboxEnvironment); ok {
		config.Sandbox = Sandbox(strings.TrimSpace(value))
	}
	if value, ok := os.LookupEnv(codexWorkspaceRootsEnvironment); ok {
		config.AllowedWorkspaceRoots = splitCodexWorkspaceRoots(value)
	}
	if value, ok := os.LookupEnv(codexEnvironmentNamesEnvironment); ok {
		config.EnvironmentNames = splitCodexList(value)
	}
	var err error
	if config.TimeoutSeconds, err = environmentInt(codexTimeoutSecondsEnvironment, config.TimeoutSeconds); err != nil {
		return RunnerConfig{}, err
	}
	if config.MaxStdoutBytes, err = environmentInt64(codexMaxStdoutBytesEnvironment, config.MaxStdoutBytes); err != nil {
		return RunnerConfig{}, err
	}
	if config.MaxStderrBytes, err = environmentInt64(codexMaxStderrBytesEnvironment, config.MaxStderrBytes); err != nil {
		return RunnerConfig{}, err
	}
	if config.MaxConcurrency, err = environmentInt(codexMaxConcurrencyEnvironment, config.MaxConcurrency); err != nil {
		return RunnerConfig{}, err
	}
	return config, nil
}

func splitCodexWorkspaceRoots(value string) []string {
	roots := make([]string, 0)
	for _, root := range filepath.SplitList(strings.TrimSpace(value)) {
		if root = strings.TrimSpace(root); root != "" {
			roots = append(roots, root)
		}
	}
	return roots
}

func splitCodexList(value string) []string {
	items := make([]string, 0)
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

func environmentInt(name string, fallback int) (int, error) {
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return parsed, nil
}

func environmentInt64(name string, fallback int64) (int64, error) {
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return parsed, nil
}

func resolveCodexRunnerConfig(config RunnerConfig) (resolvedCodexRunnerConfig, error) {
	defaults := DefaultRunnerConfig()
	config.Executable = strings.TrimSpace(config.Executable)
	if config.Executable == "" {
		config.Executable = defaults.Executable
	}
	if config.Sandbox == "" {
		config.Sandbox = defaults.Sandbox
	}
	switch config.Sandbox {
	case SandboxReadOnly, SandboxWorkspaceWrite:
	default:
		return resolvedCodexRunnerConfig{}, fmt.Errorf("Codex sandbox must be %q or %q", SandboxReadOnly, SandboxWorkspaceWrite)
	}
	if config.TimeoutSeconds == 0 {
		config.TimeoutSeconds = defaults.TimeoutSeconds
	}
	if config.TimeoutSeconds < 1 {
		return resolvedCodexRunnerConfig{}, fmt.Errorf("Codex timeout_seconds must be positive")
	}
	if config.MaxStdoutBytes == 0 {
		config.MaxStdoutBytes = defaults.MaxStdoutBytes
	}
	if config.MaxStdoutBytes < 1 {
		return resolvedCodexRunnerConfig{}, fmt.Errorf("Codex max_stdout_bytes must be positive")
	}
	if config.MaxStderrBytes == 0 {
		config.MaxStderrBytes = defaults.MaxStderrBytes
	}
	if config.MaxStderrBytes < 1 {
		return resolvedCodexRunnerConfig{}, fmt.Errorf("Codex max_stderr_bytes must be positive")
	}
	if config.MaxConcurrency == 0 {
		config.MaxConcurrency = defaults.MaxConcurrency
	}
	if config.MaxConcurrency < 1 {
		return resolvedCodexRunnerConfig{}, fmt.Errorf("Codex max_concurrency must be positive")
	}
	if config.AllowedWorkspaceRoots == nil {
		config.AllowedWorkspaceRoots = append([]string(nil), defaults.AllowedWorkspaceRoots...)
	}
	if config.EnvironmentNames == nil {
		config.EnvironmentNames = append([]string(nil), defaults.EnvironmentNames...)
	}
	environmentNames, err := normalizeCodexEnvironmentNames(config.EnvironmentNames)
	if err != nil {
		return resolvedCodexRunnerConfig{}, err
	}
	config.EnvironmentNames = environmentNames

	allowedRoots := make([]string, 0, len(config.AllowedWorkspaceRoots))
	for _, root := range config.AllowedWorkspaceRoots {
		rootPath, err := canonicalDirectory(root)
		if err != nil {
			return resolvedCodexRunnerConfig{}, fmt.Errorf("Codex allowed workspace root %q: %w", root, err)
		}
		allowedRoots = append(allowedRoots, rootPath)
	}
	if len(allowedRoots) == 0 {
		return resolvedCodexRunnerConfig{}, fmt.Errorf("Codex allowed_workspace_roots must contain at least one directory")
	}
	config.AllowedWorkspaceRoots = append([]string(nil), allowedRoots...)
	return resolvedCodexRunnerConfig{RunnerConfig: config, allowedWorkspaceRoots: allowedRoots}, nil
}

func normalizeCodexEnvironmentNames(values []string) ([]string, error) {
	names := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, name := range values {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if strings.ContainsAny(name, "=\x00\r\n ") {
			return nil, fmt.Errorf("Codex environment_names contains invalid name %q", name)
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names, nil
}

func resolveCodexWorkspace(ctx context.Context, config resolvedCodexRunnerConfig) (string, error) {
	workspace := strings.TrimSpace(core.EnvironmentVariableFromContext(ctx, codexWorkspaceEnvironment))
	if workspace == "" {
		var err error
		workspace, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	workspacePath, err := canonicalDirectory(workspace)
	if err != nil {
		return "", fmt.Errorf("Codex workspace: %w", err)
	}
	if len(config.allowedWorkspaceRoots) > 0 {
		allowed := false
		for _, root := range config.allowedWorkspaceRoots {
			if pathWithinRoot(workspacePath, root) {
				allowed = true
				break
			}
		}
		if !allowed {
			return "", fmt.Errorf("Codex workspace %q is outside allowed roots", workspacePath)
		}
	}
	return workspacePath, nil
}

func normalizeCodexBaseURL(rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("must be a valid URL: %w", err)
	}
	if parsed.Host == "" || !parsed.IsAbs() {
		return "", fmt.Errorf("must be an absolute HTTP(S) URL")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return "", fmt.Errorf("must use http or https")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("must not include URL credentials")
	}
	return rawURL, nil
}

func validateCodexModelConfig(modelID string, config core.ModelConfig) error {
	provider := openai.Provider(strings.ToLower(strings.TrimSpace(config.Provider)))
	if provider == "" {
		provider = openai.ProviderOpenAI
	}
	if provider != openai.ProviderOpenAI {
		return fmt.Errorf("Codex model %q provider %q is not supported; expected %q", modelID, provider, openai.ProviderOpenAI)
	}
	apiFormat := openai.APIFormat(strings.ToLower(strings.TrimSpace(config.APIFormat)))
	if apiFormat != openai.APIFormatResponses {
		return fmt.Errorf("Codex model %q api_format %q is not supported; expected %q", modelID, apiFormat, openai.APIFormatResponses)
	}
	return nil
}

func resolveCodexExecutable(executable string) (string, error) {
	path, err := exec.LookPath(executable)
	if err != nil {
		return "", err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if runtime.GOOS != "windows" || strings.EqualFold(filepath.Ext(path), ".exe") {
		return path, nil
	}
	patterns := []string{
		filepath.Join(filepath.Dir(path), "node_modules", "@openai", "codex", "node_modules", "@openai", "codex-win32-*", "vendor", "*", "bin", "codex.exe"),
	}
	for _, pattern := range patterns {
		matches, globErr := filepath.Glob(pattern)
		if globErr != nil {
			return "", globErr
		}
		sort.Strings(matches)
		for _, match := range matches {
			info, statErr := os.Stat(match)
			if statErr == nil && !info.IsDir() {
				return filepath.Clean(match), nil
			}
		}
	}
	return "", fmt.Errorf("resolved to non-native Windows launcher %q; configure the native codex.exe path", path)
}

func environment(ctx context.Context, config resolvedCodexRunnerConfig, apiKey string) ([]string, []string, error) {
	values := make(map[string]string)
	for _, name := range platformEnvironmentNames() {
		if value, ok := os.LookupEnv(name); ok {
			values[name] = value
		}
	}
	graphEnvironment := core.EnvironmentFromContext(ctx)
	for _, name := range config.EnvironmentNames {
		if value, ok := graphEnvironment[name]; ok {
			values[name] = value
		}
	}
	if value := strings.TrimSpace(graphEnvironment[codexWorkspaceEnvironment]); value != "" {
		values[codexWorkspaceEnvironment] = value
	}
	if strings.TrimSpace(apiKey) != "" {
		values["OPENAI_API_KEY"] = strings.TrimSpace(apiKey)
	}

	keys := make([]string, 0, len(values))
	secretValues := make([]string, 0)
	for name, value := range values {
		if name == "" || strings.ContainsAny(name, "=\x00") {
			return nil, nil, fmt.Errorf("invalid environment variable name %q", name)
		}
		if strings.Contains(value, "\x00") {
			return nil, nil, fmt.Errorf("environment variable %q contains a NUL byte", name)
		}
		keys = append(keys, name)
		if isCodexSecretEnvironmentName(name) && value != "" {
			secretValues = append(secretValues, value)
		}
	}
	sort.Strings(keys)
	environment := make([]string, 0, len(keys))
	for _, key := range keys {
		environment = append(environment, key+"="+values[key])
	}
	return environment, secretValues, nil
}

func isCodexSecretEnvironmentName(name string) bool {
	upper := strings.ToUpper(strings.TrimSpace(name))
	return strings.Contains(upper, "KEY") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD")
}

func canonicalDirectory(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", canonical)
	}
	return filepath.Clean(canonical), nil
}

func pathWithinRoot(path, root string) bool {
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
		root = strings.ToLower(root)
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative))
}
