package claude

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/dengzii/weaveflow/core"
)

const (
	claudeWorkspaceEnvironment        = "WEAVEFLOW_TOOL_WORKDIR"
	claudeExecutableEnvironment       = "WEAVEFLOW_CLAUDE_EXECUTABLE"
	claudeAccessEnvironment           = "WEAVEFLOW_CLAUDE_ACCESS"
	claudeWorkspaceRootsEnvironment   = "WEAVEFLOW_CLAUDE_WORKSPACE_ROOTS"
	claudeTimeoutSecondsEnvironment   = "WEAVEFLOW_CLAUDE_TIMEOUT_SECONDS"
	claudeMaxStdoutBytesEnvironment   = "WEAVEFLOW_CLAUDE_MAX_STDOUT_BYTES"
	claudeMaxStderrBytesEnvironment   = "WEAVEFLOW_CLAUDE_MAX_STDERR_BYTES"
	claudeMaxConcurrencyEnvironment   = "WEAVEFLOW_CLAUDE_MAX_CONCURRENCY"
	claudeModelEnvironment            = "WEAVEFLOW_CLAUDE_MODEL"
	claudeMaxBudgetUSDEnvironment     = "WEAVEFLOW_CLAUDE_MAX_BUDGET_USD"
	claudeToolsEnvironment            = "WEAVEFLOW_CLAUDE_TOOLS"
	claudeAllowedToolsEnvironment     = "WEAVEFLOW_CLAUDE_ALLOWED_TOOLS"
	claudeDisallowedToolsEnvironment  = "WEAVEFLOW_CLAUDE_DISALLOWED_TOOLS"
	claudeEnvironmentNamesEnvironment = "WEAVEFLOW_CLAUDE_ENVIRONMENT_NAMES"
	defaultTimeoutSeconds             = 900
	defaultMaxStdoutBytes             = 8 * 1024 * 1024
	defaultMaxStderrBytes             = 1024 * 1024
	defaultMaxConcurrency             = 1
	defaultMaxBudgetUSD               = 1.0
)

type WorkspaceAccess string

const (
	WorkspaceAccessReadOnly WorkspaceAccess = "read_only"
	WorkspaceAccessWrite    WorkspaceAccess = "workspace_write"
)

type RunnerConfig struct {
	Executable            string          `json:"executable,omitempty"`
	Access                WorkspaceAccess `json:"access,omitempty"`
	AllowedWorkspaceRoots []string        `json:"allowed_workspace_roots,omitempty"`
	TimeoutSeconds        int             `json:"timeout_seconds,omitempty"`
	MaxStdoutBytes        int64           `json:"max_stdout_bytes,omitempty"`
	MaxStderrBytes        int64           `json:"max_stderr_bytes,omitempty"`
	MaxConcurrency        int             `json:"max_concurrency,omitempty"`
	Model                 string          `json:"model,omitempty"`
	MaxBudgetUSD          float64         `json:"max_budget_usd,omitempty"`
	Tools                 []string        `json:"tools,omitempty"`
	AllowedTools          []string        `json:"allowed_tools,omitempty"`
	DisallowedTools       []string        `json:"disallowed_tools,omitempty"`
	EnvironmentNames      []string        `json:"environment_names,omitempty"`
}

type resolvedClaudeRunnerConfig struct {
	RunnerConfig
	allowedWorkspaceRoots []string
}

type resolvedClaudeRunConfig struct {
	resolvedClaudeRunnerConfig
	executablePath string
	workspacePath  string
	environment    []string
	secretValues   []string
}

func DefaultRunnerConfig() RunnerConfig {
	return RunnerConfig{
		Executable:            "claude",
		Access:                WorkspaceAccessReadOnly,
		AllowedWorkspaceRoots: []string{"."},
		TimeoutSeconds:        defaultTimeoutSeconds,
		MaxStdoutBytes:        defaultMaxStdoutBytes,
		MaxStderrBytes:        defaultMaxStderrBytes,
		MaxConcurrency:        defaultMaxConcurrency,
		MaxBudgetUSD:          defaultMaxBudgetUSD,
		EnvironmentNames:      []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL"},
	}
}

func RunnerConfigFromEnvironment() (RunnerConfig, error) {
	config := DefaultRunnerConfig()
	if value, ok := os.LookupEnv(claudeExecutableEnvironment); ok {
		config.Executable = strings.TrimSpace(value)
	}
	if value, ok := os.LookupEnv(claudeAccessEnvironment); ok {
		config.Access = WorkspaceAccess(strings.TrimSpace(value))
	}
	if value, ok := os.LookupEnv(claudeWorkspaceRootsEnvironment); ok {
		config.AllowedWorkspaceRoots = splitClaudeWorkspaceRoots(value)
	}
	if value, ok := os.LookupEnv(claudeModelEnvironment); ok {
		config.Model = strings.TrimSpace(value)
	}
	if value, ok := os.LookupEnv(claudeToolsEnvironment); ok {
		config.Tools = splitClaudeList(value)
	}
	if value, ok := os.LookupEnv(claudeAllowedToolsEnvironment); ok {
		config.AllowedTools = splitClaudeList(value)
	}
	if value, ok := os.LookupEnv(claudeDisallowedToolsEnvironment); ok {
		config.DisallowedTools = splitClaudeList(value)
	}
	if value, ok := os.LookupEnv(claudeEnvironmentNamesEnvironment); ok {
		config.EnvironmentNames = splitClaudeList(value)
	}
	var err error
	if config.TimeoutSeconds, err = claudeEnvironmentInt(claudeTimeoutSecondsEnvironment, config.TimeoutSeconds); err != nil {
		return RunnerConfig{}, err
	}
	if config.MaxStdoutBytes, err = claudeEnvironmentInt64(claudeMaxStdoutBytesEnvironment, config.MaxStdoutBytes); err != nil {
		return RunnerConfig{}, err
	}
	if config.MaxStderrBytes, err = claudeEnvironmentInt64(claudeMaxStderrBytesEnvironment, config.MaxStderrBytes); err != nil {
		return RunnerConfig{}, err
	}
	if config.MaxConcurrency, err = claudeEnvironmentInt(claudeMaxConcurrencyEnvironment, config.MaxConcurrency); err != nil {
		return RunnerConfig{}, err
	}
	if config.MaxBudgetUSD, err = claudeEnvironmentFloat64(claudeMaxBudgetUSDEnvironment, config.MaxBudgetUSD); err != nil {
		return RunnerConfig{}, err
	}
	return config, nil
}

func splitClaudeWorkspaceRoots(value string) []string {
	roots := make([]string, 0)
	for _, root := range filepath.SplitList(strings.TrimSpace(value)) {
		if root = strings.TrimSpace(root); root != "" {
			roots = append(roots, root)
		}
	}
	return roots
}

func splitClaudeList(value string) []string {
	items := make([]string, 0)
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

func claudeEnvironmentInt(name string, fallback int) (int, error) {
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

func claudeEnvironmentInt64(name string, fallback int64) (int64, error) {
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

func claudeEnvironmentFloat64(name string, fallback float64) (float64, error) {
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number: %w", name, err)
	}
	return parsed, nil
}

func resolveClaudeRunnerConfig(config RunnerConfig) (resolvedClaudeRunnerConfig, error) {
	defaults := DefaultRunnerConfig()
	config.Executable = strings.TrimSpace(config.Executable)
	if config.Executable == "" {
		config.Executable = defaults.Executable
	}
	if config.Access == "" {
		config.Access = defaults.Access
	}
	switch config.Access {
	case WorkspaceAccessReadOnly, WorkspaceAccessWrite:
	default:
		return resolvedClaudeRunnerConfig{}, fmt.Errorf("Claude access must be %q or %q", WorkspaceAccessReadOnly, WorkspaceAccessWrite)
	}
	if config.Access == WorkspaceAccessWrite && runtime.GOOS == "windows" {
		return resolvedClaudeRunnerConfig{}, fmt.Errorf("Claude workspace_write is unavailable on native Windows; run the server in WSL2, a container, or a VM")
	}
	if config.TimeoutSeconds == 0 {
		config.TimeoutSeconds = defaults.TimeoutSeconds
	}
	if config.TimeoutSeconds < 1 {
		return resolvedClaudeRunnerConfig{}, fmt.Errorf("Claude timeout_seconds must be positive")
	}
	if config.MaxStdoutBytes == 0 {
		config.MaxStdoutBytes = defaults.MaxStdoutBytes
	}
	if config.MaxStdoutBytes < 1 {
		return resolvedClaudeRunnerConfig{}, fmt.Errorf("Claude max_stdout_bytes must be positive")
	}
	if config.MaxStderrBytes == 0 {
		config.MaxStderrBytes = defaults.MaxStderrBytes
	}
	if config.MaxStderrBytes < 1 {
		return resolvedClaudeRunnerConfig{}, fmt.Errorf("Claude max_stderr_bytes must be positive")
	}
	if config.MaxConcurrency == 0 {
		config.MaxConcurrency = defaults.MaxConcurrency
	}
	if config.MaxConcurrency < 1 {
		return resolvedClaudeRunnerConfig{}, fmt.Errorf("Claude max_concurrency must be positive")
	}
	if config.MaxBudgetUSD == 0 {
		config.MaxBudgetUSD = defaults.MaxBudgetUSD
	}
	if config.MaxBudgetUSD < 0 {
		return resolvedClaudeRunnerConfig{}, fmt.Errorf("Claude max_budget_usd must be positive")
	}
	if config.AllowedWorkspaceRoots == nil {
		config.AllowedWorkspaceRoots = append([]string(nil), defaults.AllowedWorkspaceRoots...)
	}
	if config.EnvironmentNames == nil {
		config.EnvironmentNames = append([]string(nil), defaults.EnvironmentNames...)
	}
	if config.Tools == nil {
		config.Tools = defaultClaudeTools(config.Access)
	}
	if config.AllowedTools == nil {
		config.AllowedTools = defaultClaudeAllowedTools(config.Access)
	}
	config.Model = strings.TrimSpace(config.Model)
	config.Tools = normalizeClaudeList(config.Tools)
	config.AllowedTools = normalizeClaudeList(config.AllowedTools)
	config.DisallowedTools = normalizeClaudeList(config.DisallowedTools)
	config.EnvironmentNames = normalizeClaudeEnvironmentNames(config.EnvironmentNames)

	allowedRoots := make([]string, 0, len(config.AllowedWorkspaceRoots))
	for _, root := range config.AllowedWorkspaceRoots {
		rootPath, err := canonicalDirectory(root)
		if err != nil {
			return resolvedClaudeRunnerConfig{}, fmt.Errorf("Claude allowed workspace root %q: %w", root, err)
		}
		allowedRoots = append(allowedRoots, rootPath)
	}
	if len(allowedRoots) == 0 {
		return resolvedClaudeRunnerConfig{}, fmt.Errorf("Claude allowed_workspace_roots must contain at least one directory")
	}
	config.AllowedWorkspaceRoots = append([]string(nil), allowedRoots...)
	return resolvedClaudeRunnerConfig{RunnerConfig: config, allowedWorkspaceRoots: allowedRoots}, nil
}

func defaultClaudeTools(access WorkspaceAccess) []string {
	if access == WorkspaceAccessWrite {
		return []string{"Read", "Edit", "Write", "Glob", "Grep"}
	}
	return []string{"Read", "Glob", "Grep"}
}

func defaultClaudeAllowedTools(access WorkspaceAccess) []string {
	return defaultClaudeTools(access)
}

func normalizeClaudeList(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if strings.ContainsAny(value, "\x00\r\n") {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func normalizeClaudeEnvironmentNames(values []string) []string {
	result := normalizeClaudeList(values)
	for _, name := range result {
		if strings.ContainsAny(name, "= ") {
			return nil
		}
	}
	return result
}

func resolveClaudeWorkspace(ctx context.Context, config resolvedClaudeRunnerConfig) (string, error) {
	workspace := strings.TrimSpace(core.EnvironmentVariableFromContext(ctx, claudeWorkspaceEnvironment))
	if workspace == "" {
		var err error
		workspace, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	workspacePath, err := canonicalDirectory(workspace)
	if err != nil {
		return "", fmt.Errorf("Claude workspace: %w", err)
	}
	for _, root := range config.allowedWorkspaceRoots {
		if pathWithinRoot(workspacePath, root) {
			return workspacePath, nil
		}
	}
	return "", fmt.Errorf("Claude workspace %q is outside allowed roots", workspacePath)
}

func resolveClaudeExecutable(executable string) (string, error) {
	path, err := exec.LookPath(executable)
	if err != nil {
		return "", err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(path), ".exe") {
		return "", fmt.Errorf("resolved to non-native Windows launcher %q; configure the native claude.exe path", path)
	}
	return filepath.Clean(path), nil
}

func claudeEnvironment(config resolvedClaudeRunnerConfig) ([]string, []string, error) {
	values := make(map[string]string)
	for _, name := range platformEnvironmentNames() {
		if value, ok := os.LookupEnv(name); ok {
			values[name] = value
		}
	}
	if config.EnvironmentNames == nil {
		return nil, nil, fmt.Errorf("Claude environment_names contains an invalid name")
	}
	for _, name := range config.EnvironmentNames {
		if value, ok := os.LookupEnv(name); ok {
			values[name] = value
		}
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
		if isClaudeSecretEnvironmentName(name) && value != "" {
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

func isClaudeSecretEnvironmentName(name string) bool {
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
