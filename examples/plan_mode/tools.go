package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dengzii/weaveflow/capability/plan"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/llms"
	plannode "github.com/dengzii/weaveflow/node/plan"
	"github.com/dengzii/weaveflow/state"
	"github.com/dengzii/weaveflow/tools"
)

type VerifierConfig struct {
	Packages        []string
	AllowedPackages []string
	Files           []string
	Contains        []string
	Absent          []string
	AnalysisOnly    bool
}

type Verifier interface {
	ID() string
	Verify(context.Context, plannode.VerificationRequest) (plannode.VerificationResult, error)
}

type VerifierFactory func(VerifierConfig) (Verifier, error)

type VerifierRegistry struct {
	factories map[string]VerifierFactory
}

func NewVerifierRegistry() *VerifierRegistry {
	return &VerifierRegistry{factories: make(map[string]VerifierFactory)}
}

func (registry *VerifierRegistry) Register(id string, factory VerifierFactory) error {
	if registry == nil {
		return errors.New("verifier registry is nil")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("verifier ID is required")
	}
	if factory == nil {
		return fmt.Errorf("verifier %q factory is required", id)
	}
	if registry.factories == nil {
		registry.factories = make(map[string]VerifierFactory)
	}
	if _, exists := registry.factories[id]; exists {
		return fmt.Errorf("verifier %q is already registered", id)
	}
	registry.factories[id] = factory
	return nil
}

func (registry *VerifierRegistry) Build(id string, config VerifierConfig) (Verifier, error) {
	if registry == nil {
		return nil, errors.New("verifier registry is nil")
	}
	id = strings.TrimSpace(id)
	factory, ok := registry.factories[id]
	if !ok {
		return nil, fmt.Errorf("unknown verifier %q", id)
	}
	verifier, err := factory(config)
	if err != nil {
		return nil, err
	}
	if verifier == nil {
		return nil, fmt.Errorf("verifier %q factory returned nil", id)
	}
	if strings.TrimSpace(verifier.ID()) != id {
		return nil, fmt.Errorf("verifier %q factory returned %q", id, verifier.ID())
	}
	return verifier, nil
}

func defaultVerifierRegistry() *VerifierRegistry {
	registry := NewVerifierRegistry()
	for _, id := range []string{"go-test", "go-format-test", "file-exists", "content-match", "no-op"} {
		verifierID := id
		if err := registry.Register(verifierID, func(config VerifierConfig) (Verifier, error) {
			return newBuiltinVerifier(verifierID, config)
		}); err != nil {
			panic(err)
		}
	}
	return registry
}

type verifierFunc struct {
	id     string
	verify func(context.Context, plannode.VerificationRequest) (plannode.VerificationResult, error)
}

func (verifier verifierFunc) ID() string { return verifier.id }

func (verifier verifierFunc) Verify(ctx context.Context, request plannode.VerificationRequest) (plannode.VerificationResult, error) {
	return verifier.verify(ctx, request)
}

func toolsForProfile(profile TaskProfile, workspace string) (map[string]core.Tool, error) {
	verifier, err := newVerifier(profile.VerifierID, profile.VerifierConfig)
	if err != nil {
		return nil, err
	}
	allowedPaths, err := tools.ResolveAllowedPaths(workspace, profile.AllowedPaths)
	if err != nil {
		return nil, fmt.Errorf("profile %q allowed paths: %w", profile.ID, err)
	}
	factories := tools.BuiltinFactories()
	available := make(map[string]core.Tool, len(profile.ToolIDs))
	for _, toolID := range profile.ToolIDs {
		var tool core.Tool
		if toolID == "verify" {
			tool = newVerificationTool(verifier)
		} else {
			factory, ok := factories[toolID]
			if !ok {
				return nil, fmt.Errorf("unknown profile tool %q", toolID)
			}
			tool = factory()
		}
		if toolID == "read" && profile.MaxReadLines > 0 {
			tool = tools.WithReadLimits(tool, profile.MaxReadLines, profile.MaxReadOutputBytes)
		}
		tool = tools.WithPathScope(tool, profile.AllowedPaths)
		available[toolID] = tools.GuardWorkspaceTool(tool, workspace, allowedPaths)
	}
	return available, nil
}

func newVerificationTool(verifier Verifier) core.Tool {
	permissions := []string{"filesystem.read"}
	if verifier.ID() == "go-test" || verifier.ID() == "go-format-test" {
		permissions = []string{"process.execute"}
	}
	return core.Tool{
		Function: &llms.FunctionDefinition{
			Name:        "verify",
			Description: "Run the profile's fixed deterministic verifier. It accepts no command, path, package, or argument overrides.",
			Parameters:  state.JSONSchema{"type": "object", "additionalProperties": false},
			OutputSchema: state.JSONSchema{
				"type": "object",
				"properties": map[string]any{
					"passed":    map[string]any{"type": "boolean"},
					"summary":   map[string]any{"type": "string"},
					"retryable": map[string]any{"type": "boolean"},
				},
				"required": []string{"passed", "summary", "retryable"}, "additionalProperties": false,
			},
		},
		Handler: func(ctx context.Context, call llms.ToolCall) (llms.ToolResult, error) {
			if call.FunctionCall == nil || string(call.FunctionCall.Arguments) != "{}" && strings.TrimSpace(string(call.FunctionCall.Arguments)) != "" {
				var arguments map[string]any
				if call.FunctionCall == nil || json.Unmarshal(call.FunctionCall.Arguments, &arguments) != nil || len(arguments) != 0 {
					return llms.ToolResult{}, errors.New("verify accepts no arguments")
				}
			}
			result, err := verifier.Verify(ctx, plannode.VerificationRequest{})
			payload := map[string]any{
				"passed":    result.Status == plannode.VerificationStatusPassed,
				"summary":   result.Summary,
				"retryable": result.Retryable,
			}
			encoded, _ := json.Marshal(payload)
			toolResult := llms.ToolResult{ToolCallID: call.ID, Name: "verify", Content: string(encoded), Value: payload}
			if err != nil {
				return toolResult, err
			}
			if result.Status != plannode.VerificationStatusPassed {
				return toolResult, errors.New(result.Summary)
			}
			return toolResult, nil
		},
		Effect:      core.EffectReadOnly,
		Permissions: permissions,
		Approval:    core.ToolApprovalRequired,
	}
}

func newVerifier(id string, config VerifierConfig) (Verifier, error) {
	return defaultVerifierRegistry().Build(id, config)
}

func newBuiltinVerifier(id string, config VerifierConfig) (Verifier, error) {
	switch strings.TrimSpace(id) {
	case "go-test":
		if err := validatePackages(config.Packages, config.AllowedPackages); err != nil {
			return nil, err
		}
		return verifierFunc{id: id, verify: func(ctx context.Context, _ plannode.VerificationRequest) (plannode.VerificationResult, error) {
			return runFixedCommand(ctx, id, "go", append([]string{"test"}, config.Packages...)...)
		}}, nil
	case "go-format-test":
		if err := validatePackages(config.Packages, config.AllowedPackages); err != nil {
			return nil, err
		}
		if len(config.Files) == 0 {
			return nil, errors.New("go-format-test requires files")
		}
		return verifierFunc{id: id, verify: func(ctx context.Context, _ plannode.VerificationRequest) (plannode.VerificationResult, error) {
			files, err := resolveConfiguredFiles(ctx, config.Files)
			if err != nil {
				return failedVerification(id, err, true), nil
			}
			format := exec.CommandContext(ctx, "gofmt", append([]string{"-l"}, files...)...)
			output, err := format.CombinedOutput()
			if err != nil {
				return failedCommand(id, format.Args, output, err), nil
			}
			if strings.TrimSpace(string(output)) != "" {
				return failedVerification(id, fmt.Errorf("gofmt required for: %s", strings.TrimSpace(string(output))), true), nil
			}
			return runFixedCommand(ctx, id, "go", append([]string{"test"}, config.Packages...)...)
		}}, nil
	case "file-exists":
		if len(config.Files) == 0 {
			return nil, errors.New("file-exists requires files")
		}
		return verifierFunc{id: id, verify: func(ctx context.Context, _ plannode.VerificationRequest) (plannode.VerificationResult, error) {
			workspace, err := tools.WorkspaceFromContext(ctx)
			if err != nil {
				return failedVerification(id, err, false), nil
			}
			for _, configured := range config.Files {
				target, err := tools.SecureWorkspacePath(workspace, configured, true)
				if err != nil {
					return failedVerification(id, err, false), nil
				}
				if _, err := os.Stat(target); err != nil {
					return failedVerification(id, err, true), nil
				}
			}
			return passedVerification(id, "all configured files exist"), nil
		}}, nil
	case "content-match":
		if len(config.Files) != 1 || len(config.Contains)+len(config.Absent) == 0 {
			return nil, errors.New("content-match requires one file and at least one pattern")
		}
		return verifierFunc{id: id, verify: func(ctx context.Context, _ plannode.VerificationRequest) (plannode.VerificationResult, error) {
			workspace, err := tools.WorkspaceFromContext(ctx)
			if err != nil {
				return failedVerification(id, err, false), nil
			}
			target, err := tools.SecureWorkspacePath(workspace, config.Files[0], true)
			if err != nil {
				return failedVerification(id, err, false), nil
			}
			content, err := os.ReadFile(target)
			if err != nil {
				return failedVerification(id, err, true), nil
			}
			for _, pattern := range config.Contains {
				if !strings.Contains(string(content), pattern) {
					return failedVerification(id, fmt.Errorf("required content %q is missing", pattern), true), nil
				}
			}
			for _, pattern := range config.Absent {
				if strings.Contains(string(content), pattern) {
					return failedVerification(id, fmt.Errorf("forbidden content %q is present", pattern), true), nil
				}
			}
			return passedVerification(id, "configured content patterns match"), nil
		}}, nil
	case "no-op":
		if !config.AnalysisOnly {
			return nil, errors.New("no-op requires analysis_only")
		}
		return verifierFunc{id: id, verify: func(_ context.Context, request plannode.VerificationRequest) (plannode.VerificationResult, error) {
			if codeObjective(request.Objective) {
				return failedVerification(id, errors.New("no-op cannot verify a coding or mutation objective"), false), nil
			}
			if strings.TrimSpace(request.Step.Result) == "" {
				return failedVerification(id, errors.New("analysis step returned no result"), true), nil
			}
			if len(request.Evidence) == 0 {
				return failedVerification(id, errors.New("analysis has no read evidence"), true), nil
			}
			return passedVerification(id, "read-only analysis has observable evidence"), nil
		}}, nil
	default:
		return nil, fmt.Errorf("unknown verifier %q", id)
	}
}

func codeObjective(objective string) bool {
	objective = strings.ToLower(strings.TrimSpace(objective))
	for _, negative := range []string{"do not modify", "do not edit", "do not write", "without modifying", "without editing", "read-only"} {
		objective = strings.ReplaceAll(objective, negative, "")
	}
	for _, word := range strings.Fields(objective) {
		word = strings.Trim(word, ".,:;!?()[]{}")
		if slices.Contains([]string{"implement", "modify", "edit", "create", "write", "fix", "refactor", "update"}, word) {
			return true
		}
	}
	return false
}

func runFixedCommand(ctx context.Context, verifierID, executable string, arguments ...string) (plannode.VerificationResult, error) {
	command := exec.CommandContext(ctx, executable, arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		return failedCommand(verifierID, command.Args, output, err), nil
	}
	summary := fmt.Sprintf("%s succeeded", strings.Join(command.Args, " "))
	if strings.TrimSpace(string(output)) != "" {
		summary += ":\n" + limitToolOutput(string(output))
	}
	result := passedVerification(verifierID, summary)
	result.Evidence = []plan.Evidence{{ToolID: verifierID, Status: "succeeded", Summary: summary}}
	return result, nil
}

func failedCommand(verifierID string, args []string, output []byte, cause error) plannode.VerificationResult {
	summary := fmt.Sprintf("%s failed: %v", strings.Join(args, " "), cause)
	if strings.TrimSpace(string(output)) != "" {
		summary += ":\n" + limitToolOutput(string(output))
	}
	result := failedVerification(verifierID, errors.New(summary), true)
	result.Evidence = []plan.Evidence{{ToolID: verifierID, Status: "failed", Summary: summary, Error: cause.Error()}}
	return result
}

func passedVerification(id, summary string) plannode.VerificationResult {
	return plannode.VerificationResult{Status: plannode.VerificationStatusPassed, Summary: summary, Retryable: false}
}

func failedVerification(id string, err error, retryable bool) plannode.VerificationResult {
	status := plannode.VerificationStatusFailed
	if retryable {
		status = plannode.VerificationStatusRetry
	}
	return plannode.VerificationResult{Status: status, Summary: id + ": " + err.Error(), Retryable: retryable}
}

func validatePackages(packages, allowed []string) error {
	if len(packages) == 0 || len(allowed) == 0 {
		return errors.New("Go verifier requires package patterns and an allowlist")
	}
	for _, pattern := range packages {
		if strings.ContainsAny(pattern, " \t\r\n;&|`$()") || !slices.Contains(allowed, pattern) {
			return fmt.Errorf("package pattern %q is not allowed", pattern)
		}
	}
	return nil
}

func resolveConfiguredFiles(ctx context.Context, patterns []string) ([]string, error) {
	workspace, err := tools.WorkspaceFromContext(ctx)
	if err != nil {
		return nil, err
	}
	files := make([]string, 0)
	for _, pattern := range patterns {
		if filepath.IsAbs(pattern) || strings.Contains(pattern, "..") {
			return nil, fmt.Errorf("file pattern %q escapes workspace", pattern)
		}
		matches, err := filepath.Glob(filepath.Join(workspace, filepath.FromSlash(pattern)))
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("file pattern %q matched no files", pattern)
		}
		for _, match := range matches {
			target, err := tools.SecureWorkspacePath(workspace, match, true)
			if err != nil {
				return nil, err
			}
			files = append(files, target)
		}
	}
	return files, nil
}

func limitToolOutput(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 16*1024 {
		return value[:16*1024] + "..."
	}
	return value
}
