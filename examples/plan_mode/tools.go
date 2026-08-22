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

type verifierFunc struct {
	id     string
	verify func(context.Context, plannode.VerificationRequest) (plannode.VerificationResult, error)
}

func (verifier verifierFunc) ID() string { return verifier.id }

func (verifier verifierFunc) Verify(ctx context.Context, request plannode.VerificationRequest) (plannode.VerificationResult, error) {
	return verifier.verify(ctx, request)
}

func safeToolFactories() map[string]func() core.Tool {
	return map[string]func() core.Tool{
		"read":    tools.NewRead,
		"outline": tools.NewOutline,
		"write":   tools.NewWrite,
		"edit":    tools.NewEdit,
		"grep":    tools.NewGrep,
		"glob":    tools.NewGlob,
		"verify":  func() core.Tool { return core.Tool{} },
	}
}

func toolsForProfile(profile TaskProfile, workspace string) (map[string]core.Tool, error) {
	verifier, err := newVerifier(profile.VerifierID, profile.VerifierConfig)
	if err != nil {
		return nil, err
	}
	factories := safeToolFactories()
	available := make(map[string]core.Tool, len(profile.ToolIDs))
	for _, toolID := range profile.ToolIDs {
		factory, ok := factories[toolID]
		if !ok {
			return nil, fmt.Errorf("unknown profile tool %q", toolID)
		}
		var tool core.Tool
		if toolID == "verify" {
			tool = newVerificationTool(verifier)
		} else {
			tool = factory()
		}
		if toolID == "read" && profile.MaxReadLines > 0 {
			tool = boundReadTool(tool, profile.MaxReadLines, profile.MaxReadOutputBytes)
		}
		available[toolID] = guardWorkspaceTool(tool, workspace)
	}
	return available, nil
}

func boundReadTool(tool core.Tool, maxLines int, maxOutputBytes int) core.Tool {
	if maxLines <= 0 {
		return tool
	}
	if tool.Function != nil {
		definition := *tool.Function
		definition.Description = strings.TrimSpace(definition.Description) + fmt.Sprintf("\nProfile limit: at most %d lines per call.", maxLines)
		if properties, ok := definition.Parameters["properties"].(map[string]any); ok {
			if limitSchema, ok := properties["limit"].(map[string]any); ok {
				limitSchema["maximum"] = maxLines
			}
		}
		tool.Function = &definition
	}
	handler := tool.Handler
	tool.Handler = func(ctx context.Context, call llms.ToolCall) (llms.ToolResult, error) {
		if call.FunctionCall == nil {
			return llms.ToolResult{}, errors.New("read call has no function payload")
		}
		var arguments map[string]any
		if err := json.Unmarshal(call.FunctionCall.Arguments, &arguments); err != nil {
			return llms.ToolResult{}, fmt.Errorf("decode bounded read arguments: %w", err)
		}
		limit := intArgument(arguments["limit"])
		if limit <= 0 {
			arguments["limit"] = maxLines
		} else if limit > maxLines {
			return llms.ToolResult{}, fmt.Errorf("read limit %d exceeds profile maximum %d", limit, maxLines)
		}
		encoded, err := json.Marshal(arguments)
		if err != nil {
			return llms.ToolResult{}, fmt.Errorf("encode bounded read arguments: %w", err)
		}
		functionCall := *call.FunctionCall
		functionCall.Arguments = encoded
		call.FunctionCall = &functionCall
		result, err := handler(ctx, call)
		if maxOutputBytes > 0 {
			result.Content = limitProfileToolOutput(result.Content, maxOutputBytes)
			if _, ok := result.Value.(string); ok {
				result.Value = result.Content
			}
		}
		return result, err
	}
	return tool
}

func intArgument(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	default:
		return 0
	}
}

func limitProfileToolOutput(value string, maximum int) string {
	if maximum <= 0 || len(value) <= maximum {
		return value
	}
	prefix := strings.ToValidUTF8(value[:maximum], "")
	return strings.TrimSpace(prefix) + "\n[truncated by profile output limit]"
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
			workspace, err := workspaceFromContext(ctx)
			if err != nil {
				return failedVerification(id, err, false), nil
			}
			for _, configured := range config.Files {
				target, err := secureWorkspacePath(workspace, configured, true)
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
			workspace, err := workspaceFromContext(ctx)
			if err != nil {
				return failedVerification(id, err, false), nil
			}
			target, err := secureWorkspacePath(workspace, config.Files[0], true)
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
	workspace, err := workspaceFromContext(ctx)
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
			target, err := secureWorkspacePath(workspace, match, true)
			if err != nil {
				return nil, err
			}
			files = append(files, target)
		}
	}
	return files, nil
}

func guardWorkspaceTool(tool core.Tool, workspace string) core.Tool {
	handler := tool.Handler
	tool.Handler = func(ctx context.Context, call llms.ToolCall) (llms.ToolResult, error) {
		if err := validateToolPaths(workspace, call); err != nil {
			return llms.ToolResult{}, err
		}
		return handler(ctx, call)
	}
	return tool
}

func validateToolPaths(workspace string, call llms.ToolCall) error {
	if call.FunctionCall == nil {
		return errors.New("tool call has no function payload")
	}
	var arguments map[string]any
	if err := json.Unmarshal(call.FunctionCall.Arguments, &arguments); err != nil {
		return fmt.Errorf("decode tool paths: %w", err)
	}
	for _, key := range []string{"file_path", "path"} {
		value, _ := arguments[key].(string)
		if strings.TrimSpace(value) == "" {
			continue
		}
		mustExist := call.FunctionCall.Name != "write"
		if _, err := secureWorkspacePath(workspace, value, mustExist); err != nil {
			return err
		}
	}
	return nil
}

func secureWorkspacePath(workspace, value string, mustExist bool) (string, error) {
	root, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	target := filepath.Clean(value)
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if !pathWithin(root, target) {
		return "", errors.New("path escapes workspace")
	}
	resolved := target
	if mustExist {
		resolved, err = filepath.EvalSymlinks(target)
		if err != nil {
			return "", err
		}
	} else {
		if _, statErr := os.Lstat(target); statErr == nil {
			resolved, err = filepath.EvalSymlinks(target)
			if err != nil {
				return "", err
			}
			if !pathWithin(root, resolved) {
				return "", errors.New("path resolves outside workspace")
			}
			return target, nil
		}
		ancestor := filepath.Dir(target)
		for {
			if _, statErr := os.Lstat(ancestor); statErr == nil {
				break
			}
			parent := filepath.Dir(ancestor)
			if parent == ancestor {
				return "", errors.New("cannot resolve workspace path")
			}
			ancestor = parent
		}
		resolvedAncestor, resolveErr := filepath.EvalSymlinks(ancestor)
		if resolveErr != nil {
			return "", resolveErr
		}
		resolved = filepath.Join(resolvedAncestor, strings.TrimPrefix(target, ancestor+string(os.PathSeparator)))
	}
	if !pathWithin(root, resolved) {
		return "", errors.New("path resolves outside workspace")
	}
	return target, nil
}

func pathWithin(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func workspaceFromContext(ctx context.Context) (string, error) {
	workspace := strings.TrimSpace(core.EnvironmentVariableFromContext(ctx, "WEAVEFLOW_TOOL_WORKDIR"))
	if workspace == "" {
		return os.Getwd()
	}
	return filepath.Abs(workspace)
}

func limitToolOutput(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 16*1024 {
		return value[:16*1024] + "..."
	}
	return value
}
