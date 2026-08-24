package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dengzii/weaveflow/tools"
)

type ApprovalPolicy string

const (
	ApprovalDeny       ApprovalPolicy = "deny"
	ApprovalConfigured ApprovalPolicy = "configured"
)

type TaskProfile struct {
	ID                  string
	Description         string
	DefaultObjective    string
	PlannerPrompt       string
	WorkerPrompt        string
	FinalizerPrompt     string
	SynthesisPrompt     string
	CriticPrompt        string
	GroundedCritic      bool
	RequireEvidenceRefs bool
	ToolIDs             []string
	MaxSteps            int
	MaxReplans          int
	MaxIterations       int
	MaxStepAttempts     int
	TotalTimeout        time.Duration
	ModelTimeout        time.Duration
	ModelRetries        int
	MaxReadLines        int
	MaxReadOutputBytes  int
	AllowedPaths        []string
	Permissions         []string
	ApprovalPolicy      ApprovalPolicy
	ApprovedTools       []string
	VerifierID          string
	VerifierConfig      VerifierConfig
}

type ProfileRegistry struct {
	profiles  map[string]TaskProfile
	verifiers *VerifierRegistry
}

func NewProfileRegistry(verifiers *VerifierRegistry) *ProfileRegistry {
	if verifiers == nil {
		verifiers = defaultVerifierRegistry()
	}
	return &ProfileRegistry{profiles: make(map[string]TaskProfile), verifiers: verifiers}
}

func (registry *ProfileRegistry) Register(profile TaskProfile) error {
	if registry == nil {
		return errors.New("profile registry is nil")
	}
	if err := profile.ValidateWith(registry.verifiers); err != nil {
		return err
	}
	if registry.profiles == nil {
		registry.profiles = make(map[string]TaskProfile)
	}
	if _, exists := registry.profiles[profile.ID]; exists {
		return fmt.Errorf("profile %q is already registered", profile.ID)
	}
	registry.profiles[profile.ID] = cloneProfile(profile)
	return nil
}

func (registry *ProfileRegistry) Lookup(id string) (TaskProfile, error) {
	if registry == nil {
		return TaskProfile{}, errors.New("profile registry is nil")
	}
	id = strings.TrimSpace(id)
	profile, ok := registry.profiles[id]
	if !ok {
		return TaskProfile{}, fmt.Errorf("unknown profile %q; available profiles: %s", id, strings.Join(registry.IDs(), ", "))
	}
	return cloneProfile(profile), nil
}

func (registry *ProfileRegistry) IDs() []string {
	if registry == nil {
		return nil
	}
	ids := make([]string, 0, len(registry.profiles))
	for id := range registry.profiles {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (registry *ProfileRegistry) Profiles() map[string]TaskProfile {
	if registry == nil {
		return nil
	}
	profiles := make(map[string]TaskProfile, len(registry.profiles))
	for id, profile := range registry.profiles {
		profiles[id] = cloneProfile(profile)
	}
	return profiles
}

func cloneProfile(profile TaskProfile) TaskProfile {
	profile.ToolIDs = append([]string(nil), profile.ToolIDs...)
	profile.AllowedPaths = append([]string(nil), profile.AllowedPaths...)
	profile.Permissions = append([]string(nil), profile.Permissions...)
	profile.ApprovedTools = append([]string(nil), profile.ApprovedTools...)
	profile.VerifierConfig.Packages = append([]string(nil), profile.VerifierConfig.Packages...)
	profile.VerifierConfig.AllowedPackages = append([]string(nil), profile.VerifierConfig.AllowedPackages...)
	profile.VerifierConfig.Files = append([]string(nil), profile.VerifierConfig.Files...)
	profile.VerifierConfig.Contains = append([]string(nil), profile.VerifierConfig.Contains...)
	profile.VerifierConfig.Absent = append([]string(nil), profile.VerifierConfig.Absent...)
	return profile
}

func (profile TaskProfile) Validate() error {
	return profile.ValidateWith(defaultVerifierRegistry())
}

func (profile TaskProfile) ValidateWith(verifiers *VerifierRegistry) error {
	if verifiers == nil {
		return errors.New("verifier registry is required")
	}
	if strings.TrimSpace(profile.ID) == "" {
		return errors.New("profile ID is required")
	}
	if strings.TrimSpace(profile.DefaultObjective) == "" {
		return fmt.Errorf("profile %q default objective is required", profile.ID)
	}
	if strings.TrimSpace(profile.PlannerPrompt) == "" || strings.TrimSpace(profile.WorkerPrompt) == "" || strings.TrimSpace(profile.FinalizerPrompt) == "" || strings.TrimSpace(profile.SynthesisPrompt) == "" {
		return fmt.Errorf("profile %q requires planner, worker, finalizer, and synthesis prompts", profile.ID)
	}
	if profile.GroundedCritic && strings.TrimSpace(profile.CriticPrompt) == "" {
		return fmt.Errorf("profile %q grounded critic prompt is required", profile.ID)
	}
	if profile.MaxSteps <= 0 || profile.MaxIterations <= 0 || profile.MaxStepAttempts <= 0 || profile.MaxReplans < 0 {
		return fmt.Errorf("profile %q has invalid step, iteration, attempt, or replan budget", profile.ID)
	}
	if profile.TotalTimeout <= 0 || profile.ModelTimeout <= 0 || profile.ModelRetries <= 0 || profile.ModelTimeout > profile.TotalTimeout {
		return fmt.Errorf("profile %q has invalid timeout or model retry budget", profile.ID)
	}
	if profile.MaxReadLines < 0 || profile.MaxReadOutputBytes < 0 {
		return fmt.Errorf("profile %q has invalid read limits", profile.ID)
	}
	for _, allowedPath := range profile.AllowedPaths {
		allowedPath = strings.TrimSpace(allowedPath)
		if allowedPath == "" || filepath.IsAbs(allowedPath) {
			return fmt.Errorf("profile %q has invalid allowed path %q", profile.ID, allowedPath)
		}
		cleaned := filepath.Clean(filepath.FromSlash(allowedPath))
		if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
			return fmt.Errorf("profile %q allowed path %q escapes workspace", profile.ID, allowedPath)
		}
	}
	knownTools := tools.BuiltinFactories()
	toolSet := make(map[string]struct{}, len(profile.ToolIDs))
	for _, toolID := range profile.ToolIDs {
		toolID = strings.TrimSpace(toolID)
		if _, ok := knownTools[toolID]; !ok && toolID != "verify" {
			return fmt.Errorf("profile %q references unknown tool %q", profile.ID, toolID)
		}
		toolSet[toolID] = struct{}{}
	}
	if len(toolSet) == 0 {
		return fmt.Errorf("profile %q has no tools", profile.ID)
	}
	if profile.MaxReadLines > 0 {
		if _, readable := toolSet["read"]; !readable {
			return fmt.Errorf("profile %q configures read limits without the read tool", profile.ID)
		}
	}
	permissionSet := stringSet(profile.Permissions)
	if _, executable := permissionSet["process.execute"]; executable && profile.VerifierID != "go-test" && profile.VerifierID != "go-format-test" {
		return fmt.Errorf("profile %q grants process.execute without a fixed Go verifier", profile.ID)
	}
	if _, writable := permissionSet["filesystem.write"]; writable {
		if len(profile.AllowedPaths) == 0 {
			return fmt.Errorf("profile %q requires allowed paths for filesystem.write", profile.ID)
		}
		if profile.ApprovalPolicy != ApprovalConfigured {
			return fmt.Errorf("profile %q grants filesystem.write without configured approval", profile.ID)
		}
		for _, required := range []string{"write", "edit"} {
			if _, enabled := toolSet[required]; enabled {
				if _, approved := stringSet(profile.ApprovedTools)[required]; !approved {
					return fmt.Errorf("profile %q enables %s without explicit approval", profile.ID, required)
				}
			}
		}
	}
	if profile.ApprovalPolicy != ApprovalDeny && profile.ApprovalPolicy != ApprovalConfigured {
		return fmt.Errorf("profile %q has unknown approval policy %q", profile.ID, profile.ApprovalPolicy)
	}
	verifier, err := verifiers.Build(profile.VerifierID, profile.VerifierConfig)
	if err != nil {
		return fmt.Errorf("profile %q verifier: %w", profile.ID, err)
	}
	if verifier == nil {
		return fmt.Errorf("profile %q verifier is required", profile.ID)
	}
	if profile.VerifierID == "no-op" {
		if _, writes := permissionSet["filesystem.write"]; writes {
			return fmt.Errorf("profile %q cannot use no-op verification for a writable task", profile.ID)
		}
		if _, executes := permissionSet["process.execute"]; executes {
			return fmt.Errorf("profile %q cannot use no-op verification for a process task", profile.ID)
		}
	}
	return nil
}

func defaultProfileRegistry() *ProfileRegistry {
	base := TaskProfile{
		PlannerPrompt:       defaultPlannerPrompt,
		WorkerPrompt:        defaultWorkerPrompt,
		FinalizerPrompt:     defaultFinalizerPrompt,
		SynthesisPrompt:     defaultSynthesisPrompt,
		CriticPrompt:        defaultCriticPrompt,
		RequireEvidenceRefs: true,
		MaxSteps:            4,
		MaxReplans:          1,
		MaxIterations:       6,
		MaxStepAttempts:     2,
		TotalTimeout:        15 * time.Minute,
		ModelTimeout:        90 * time.Second,
		ModelRetries:        2,
	}
	tinyScript := base
	tinyScript.ID = "tiny-script"
	tinyScript.Description = "Implement and verify the TinyScript regression fixture."
	tinyScript.DefaultObjective = tinyScriptObjective
	tinyScript.PlannerPrompt += "\nFor TinyScript, keep every step scoped to examples/plan_mode/tiny_script and choose go-format-test for the final validation step."
	tinyScript.MaxSteps = 1
	tinyScript.ToolIDs = []string{"read", "write", "edit", "grep", "glob", "verify"}
	tinyScript.Permissions = []string{"filesystem.read", "filesystem.write", "process.execute"}
	tinyScript.AllowedPaths = []string{"examples/plan_mode/tiny_script"}
	tinyScript.ApprovalPolicy = ApprovalConfigured
	tinyScript.ApprovedTools = []string{"write", "edit", "verify"}
	tinyScript.VerifierID = "go-format-test"
	tinyScript.VerifierConfig = VerifierConfig{
		Files:           []string{"examples/plan_mode/tiny_script/*.go"},
		Packages:        []string{"./examples/plan_mode/tiny_script/..."},
		AllowedPackages: []string{"./examples/plan_mode/tiny_script/..."},
	}

	coding := base
	coding.ID = "coding-go"
	coding.Description = "Implement a scoped Go change and prove it with focused tests."
	coding.DefaultObjective = "Implement NormalizeTags in examples/plan_mode/fixtures/coding_go so the existing table-driven tests pass. It must trim whitespace, lowercase tags, omit empty values, deduplicate normalized tags while preserving first occurrence order, return a non-nil empty slice for nil input, and never mutate the input. Inspect the existing test, add the minimal implementation, run the fixed verifier, and fix failures until it passes."
	coding.PlannerPrompt += "\nFor Go coding, plan inspection, implementation, focused tests, and correction of failures."
	coding.MaxSteps = 1
	coding.ToolIDs = []string{"read", "write", "edit", "grep", "glob", "verify"}
	coding.Permissions = []string{"filesystem.read", "filesystem.write", "process.execute"}
	coding.AllowedPaths = []string{"examples/plan_mode/fixtures/coding_go"}
	coding.ApprovalPolicy = ApprovalConfigured
	coding.ApprovedTools = []string{"write", "edit", "verify"}
	coding.VerifierID = "go-test"
	coding.VerifierConfig = VerifierConfig{Packages: []string{"./examples/plan_mode/fixtures/coding_go/..."}, AllowedPackages: []string{"./examples/plan_mode/fixtures/coding_go/..."}}

	documentation := base
	documentation.ID = "documentation"
	documentation.Description = "Update a repository document and verify required content."
	documentation.DefaultObjective = "Update examples/plan_mode/README.md to explain the profile-based plan example, its CLI, and its safety boundaries."
	documentation.PlannerPrompt += "\nFor documentation, require a concrete target file and content-match acceptance criteria."
	documentation.MaxSteps = 1
	documentation.MaxIterations = 5
	documentation.ToolIDs = []string{"read", "write", "edit", "grep", "glob", "verify"}
	documentation.Permissions = []string{"filesystem.read", "filesystem.write"}
	documentation.AllowedPaths = []string{"examples/plan_mode"}
	documentation.ApprovalPolicy = ApprovalConfigured
	documentation.ApprovedTools = []string{"write", "edit", "verify"}
	documentation.VerifierID = "content-match"
	documentation.VerifierConfig = VerifierConfig{Files: []string{"examples/plan_mode/README.md"}, Contains: []string{"profiles", "-profile", "safety"}}
	documentation.GroundedCritic = true
	documentation.MaxReadLines = 320
	documentation.MaxReadOutputBytes = 16 * 1024

	analysis := base
	analysis.ID = "analysis"
	analysis.Description = "Read-only repository analysis with no mutation tools."
	analysis.DefaultObjective = "Analyze examples/plan_mode and explain how graph routing, tool evidence, and deterministic verification prevent unsupported completion claims. Do not modify files."
	analysis.PlannerPrompt += "\nThis is read-only analysis: never propose edits or process execution, and require citations to inspected repository files."
	analysis.MaxSteps = 3
	analysis.MaxIterations = 4
	analysis.TotalTimeout = 10 * time.Minute
	analysis.ToolIDs = []string{"outline", "read", "grep", "glob"}
	analysis.Permissions = []string{"filesystem.read"}
	analysis.AllowedPaths = []string{"."}
	analysis.ApprovalPolicy = ApprovalDeny
	analysis.ApprovedTools = nil
	analysis.VerifierID = "no-op"
	analysis.VerifierConfig = VerifierConfig{AnalysisOnly: true}
	analysis.GroundedCritic = true
	analysis.MaxReadLines = 240
	analysis.MaxReadOutputBytes = 12 * 1024

	multiStep := analysis
	multiStep.ID = "multi-step"
	multiStep.Description = "Multi-step read-only repository review with grounded evidence."
	multiStep.DefaultObjective = "Review examples/plan_mode as a reusable Plan-Execute-Review pattern. Use separate steps for graph topology, tool and permission boundaries, and checkpoint/replan behavior. Produce an evidence-backed report without modifying files."
	multiStep.PlannerPrompt += "\nThis profile is intentionally multi-step: keep topology, safety, and recovery as separate steps when the objective supports them."
	multiStep.MaxSteps = 4
	multiStep.MaxIterations = 5
	multiStep.TotalTimeout = 12 * time.Minute

	profiles := map[string]TaskProfile{
		tinyScript.ID:    tinyScript,
		coding.ID:        coding,
		documentation.ID: documentation,
		analysis.ID:      analysis,
		multiStep.ID:     multiStep,
	}
	registry := NewProfileRegistry(defaultVerifierRegistry())
	for _, profile := range profiles {
		if err := registry.Register(profile); err != nil {
			panic(err)
		}
	}
	return registry
}

func profileByID(id string) (TaskProfile, error) {
	return defaultProfileRegistry().Lookup(id)
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result[value] = struct{}{}
		}
	}
	return result
}

const defaultPlannerPrompt = `You are the planning component of a configurable plan-mode agent.
Return strict JSON matching the provided schema. Use only available_tools. Every step needs observable deliverables, falsifiable acceptance criteria, and a verification strategy. Keep successful evidence when replanning.
The summary must describe exactly the returned steps and must never claim a different step count. The final step must directly cover the objective, not merely preliminary inspection.`

const defaultWorkerPrompt = `Execute exactly one plan step. Inspect before changing anything, use only the available tools, and collect concrete evidence. Prefer outline and bounded read ranges over reading entire large files. After changing a factual document, reread the relevant source and changed ranges. Never claim a command passed without a successful tool result. Stop when the deliverables and acceptance criteria can be verified.`

const defaultFinalizerPrompt = `Summarize only the current step using tool evidence. Do not call tools and do not turn an unsupported claim into evidence.`

const defaultSynthesisPrompt = `Synthesize a concise final answer from verified plan step results. Cite the supplied evidence refs for material factual claims, acknowledge failures, and never claim success when a verifier rejected a step.`

const defaultCriticPrompt = `Audit the step result only against the enumerated evidence.
Reject unsupported or contradicted factual claims, including incorrect numeric limits, topology, permissions, and runtime behavior.
Every supported claim must cite one or more valid evidence refs. Do not use outside knowledge.
Historical failed checks count as resolved only when later evidence directly proves the same issue was corrected.
Return strict JSON matching the supplied schema.`

const tinyScriptObjective = `Implement a small typed scripting language in examples/plan_mode/tiny_script.

The language must support typed variables, functions, expressions, if/else, while loops, static type checking with source locations, a public interpreter API, and table-driven tests. Inspect the repository, implement the code, run the fixed TinyScript formatter/test verifier, and fix failures until it passes. Do not change unrelated files.`
