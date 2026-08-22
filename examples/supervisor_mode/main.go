package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dengzii/weaveflow/builtin"
	supervisorcap "github.com/dengzii/weaveflow/capability/supervisor"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	wfgraph "github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/llms"
	"github.com/dengzii/weaveflow/llms/openai"
	supervisornode "github.com/dengzii/weaveflow/node/supervisor"
	"github.com/dengzii/weaveflow/state"
	"github.com/dengzii/weaveflow/tools"
)

const defaultObjective = `Implement a self-contained typed mini scripting language in Go using only the standard library.

Acceptance criteria:
- Define a small, documented syntax with typed declarations such as let total: int = 0; and reassignment; typed functions such as fn add(a int, b int) -> int { return a + b; }; first-class function values; user-defined and built-in function calls; if/else blocks; while loops; literals; grouping; unary operators; arithmetic; comparisons; and &&/|| boolean expressions with normal precedence.
- Lex and parse source into an AST with useful source spans and clear syntax errors.
- Perform a static type-checking pass before execution. Support at least int, bool, string, and function types; reject undefined names, duplicate declarations, invalid assignments, bad argument counts/types, invalid operators, and non-boolean conditions with precise diagnostics.
- Evaluate only type-checked programs with lexical environments, returns, loop control, recursion, function calls, runtime errors, a configurable loop limit, and safe print, len, and toInt built-ins. Never panic on user input.
- Include a complete runnable Go implementation, table-driven tests for successful programs and every major error class, and a short example script. The implementation must be compile-ready rather than pseudocode.

Return the file tree, complete source files, tests, and exact go test/go run commands. Prefer a compact package that another developer can copy into a fresh Go module.`

//go:embed graph.json
var supervisorGraphJSON []byte

func main() {
	objective := strings.TrimSpace(strings.Join(os.Args[1:], " "))
	if objective == "" {
		objective = defaultObjective
	}

	configuredModel, err := modelFromEnvironment()
	must(err)
	model := retryingModel{model: configuredModel, attempts: 8, timeout: 10 * time.Minute, delay: 5 * time.Second, maxDelay: 30 * time.Second}
	graph, err := newSupervisorGraph()
	must(err)
	workspace, err := newGeneratedWorkspace()
	must(err)

	ctx := core.WithModel(context.Background(), model)
	ctx = core.WithTools(ctx, map[string]core.Tool{
		"edit":                 newRepairableEditTool(workspace),
		"glob":                 tools.NewGlob(),
		"go_validate_frontend": newGoValidateTool(workspace, "frontend"),
		"go_validate_package":  newGoValidateTool(workspace, "package"),
		"go_validate_source":   newGoValidateTool(workspace, "source"),
		"read":                 tools.NewRead(),
		"write":                tools.NewWrite(),
	})
	ctx = core.WithEnvironment(ctx, map[string]string{
		"WEAVEFLOW_TOOL_WORKDIR":              workspace,
		"WEAVEFLOW_TOOL_SKIP_WORKSPACE_CHECK": "false",
	})
	ctx = core.WithToolPermissions(ctx, "filesystem.read", "filesystem.write", "process.execute")
	ctx = core.WithToolApprover(ctx, core.ToolApproverFunc(func(context.Context, core.ToolApprovalRequest) (core.ToolApprovalDecision, error) {
		return core.ToolApprovalDecision{ApprovalID: "supervisor-mode", Approved: true, Actor: "supervisor-mode", Reason: "isolated generated workspace"}, nil
	}))
	initial := state.FromShared(map[string]any{
		"request": map[string]any{"input": objective},
	})
	finalState, err := graph.Run(ctx, initial)
	must(err)

	fmt.Println("objective:", objective)
	if raw, ok := state.ReadPath(finalState, state.Shared("supervisor", supervisornode.SupervisorFieldHistory).String()); ok {
		fmt.Println("delegations:")
		for _, turn := range decodeSupervisorHistory(raw) {
			fmt.Printf("  %d. %s\n     task: %s\n     result: %s\n", turn.Turn, turn.WorkerID, turn.Task, turn.Result)
		}
	}
	answer, err := verifyAndBundleWorkspace(workspace)
	must(err)
	fmt.Println("\nfinal answer:")
	fmt.Println(answer)
	fmt.Println("\nverified workspace:", workspace)
}

func newGeneratedWorkspace() (string, error) {
	if err := os.MkdirAll(".local", 0o755); err != nil {
		return "", err
	}
	workspace, err := os.MkdirTemp(".local", "supervisor-mode-")
	if err != nil {
		return "", err
	}
	return filepath.Abs(workspace)
}

type repairableEditRequest struct {
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

type repairableEditResponse struct {
	Passed       bool   `json:"passed"`
	Action       string `json:"action"`
	Path         string `json:"path"`
	Workspace    string `json:"workspace"`
	Replacements int    `json:"replacements"`
	Size         int    `json:"size"`
	Message      string `json:"message"`
}

func newRepairableEditTool(workspace string) core.Tool {
	tool := core.NewTool(&llms.FunctionDefinition{
		Name: "edit",
		Description: "Performs exact string replacements in files. Missing or ambiguous matches return passed=false so you can read the current file and retry.\n\n" +
			"Usage:\n" +
			"- The edit is not applied if old_string is not unique and replace_all is false.\n" +
			"- Use replace_all to change every instance of old_string.",
		Parameters: state.JSONSchema{
			"type": "object",
			"properties": state.JSONSchema{
				"file_path": state.JSONSchema{
					"type":        "string",
					"description": "Workspace-relative file to modify.",
				},
				"old_string": state.JSONSchema{
					"type":        "string",
					"description": "The exact text to replace.",
				},
				"new_string": state.JSONSchema{
					"type":        "string",
					"description": "The replacement text, which must differ from old_string.",
				},
				"replace_all": state.JSONSchema{
					"type":        "boolean",
					"default":     false,
					"description": "Replace all occurrences of old_string.",
				},
			},
			"required":             []string{"file_path", "old_string", "new_string"},
			"additionalProperties": false,
		},
		OutputSchema: state.JSONSchema{
			"type": "object",
			"properties": state.JSONSchema{
				"passed":       state.JSONSchema{"type": "boolean"},
				"action":       state.JSONSchema{"type": "string"},
				"path":         state.JSONSchema{"type": "string"},
				"workspace":    state.JSONSchema{"type": "string"},
				"replacements": state.JSONSchema{"type": "integer"},
				"size":         state.JSONSchema{"type": "integer"},
				"message":      state.JSONSchema{"type": "string"},
			},
			"required":             []string{"passed", "action", "path", "workspace", "replacements", "size", "message"},
			"additionalProperties": false,
		},
	}, func(_ context.Context, call llms.ToolCall) (llms.ToolResult, error) {
		var request repairableEditRequest
		if err := core.DecodeToolArguments(call, &request); err != nil {
			return llms.ToolResult{}, fmt.Errorf("edit input: %w", err)
		}
		request.FilePath = strings.TrimSpace(request.FilePath)
		if request.FilePath == "" {
			return llms.ToolResult{}, errors.New("file_path is required")
		}
		inputFailure := repairableEditResponse{
			Action:    "edit",
			Path:      filepath.ToSlash(request.FilePath),
			Workspace: filepath.ToSlash(workspace),
		}
		if request.OldString == "" {
			inputFailure.Message = "edit not applied: old_string is required; read the file and retry with exact current text"
			return repairableEditToolResult(call, inputFailure), nil
		}
		if request.OldString == request.NewString {
			inputFailure.Message = "edit not applied: new_string must be different from old_string"
			return repairableEditToolResult(call, inputFailure), nil
		}

		target, relativePath, err := resolveGeneratedEditPath(workspace, request.FilePath)
		if err != nil {
			return llms.ToolResult{}, err
		}
		data, err := os.ReadFile(target)
		if err != nil {
			return llms.ToolResult{}, fmt.Errorf("read edit target: %w", err)
		}
		content := string(data)
		matches := strings.Count(content, request.OldString)
		response := repairableEditResponse{
			Action:       "edit",
			Path:         relativePath,
			Workspace:    filepath.ToSlash(workspace),
			Replacements: matches,
			Size:         len(data),
		}
		if matches == 0 {
			response.Message = "edit not applied: old_string not found; read the file and retry with exact current text"
			return repairableEditToolResult(call, response), nil
		}
		if matches > 1 && !request.ReplaceAll {
			response.Message = fmt.Sprintf("edit not applied: old_string is not unique; found %d occurrences; provide more context or set replace_all", matches)
			return repairableEditToolResult(call, response), nil
		}

		replaceCount := 1
		if request.ReplaceAll {
			replaceCount = -1
		}
		updated := strings.Replace(content, request.OldString, request.NewString, replaceCount)
		if err := os.WriteFile(target, []byte(updated), 0o644); err != nil {
			return llms.ToolResult{}, fmt.Errorf("write edit target: %w", err)
		}
		response.Passed = true
		response.Size = len(updated)
		if !request.ReplaceAll {
			response.Replacements = 1
		}
		response.Message = fmt.Sprintf("edit applied: replaced %d occurrence(s)", response.Replacements)
		return repairableEditToolResult(call, response), nil
	})
	tool.Permissions = []string{"filesystem.write"}
	tool.Approval = core.ToolApprovalRequired
	tool.Effect = core.EffectIdempotentWrite
	return tool
}

func resolveGeneratedEditPath(workspace, rawPath string) (string, string, error) {
	if filepath.IsAbs(rawPath) || filepath.VolumeName(rawPath) != "" || strings.HasPrefix(rawPath, `/`) || strings.HasPrefix(rawPath, `\\`) {
		return "", "", fmt.Errorf("edit file_path must be workspace-relative: %q", rawPath)
	}
	cleanPath := filepath.Clean(rawPath)
	if cleanPath == "." || cleanPath == ".." || strings.HasPrefix(cleanPath, ".."+string(os.PathSeparator)) {
		return "", "", fmt.Errorf("edit file_path must stay within the workspace: %q", rawPath)
	}
	absoluteWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", "", fmt.Errorf("resolve edit workspace: %w", err)
	}
	target, err := filepath.Abs(filepath.Join(absoluteWorkspace, cleanPath))
	if err != nil {
		return "", "", fmt.Errorf("resolve edit target: %w", err)
	}
	relativePath, err := filepath.Rel(absoluteWorkspace, target)
	if err != nil || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(os.PathSeparator)) {
		return "", "", fmt.Errorf("edit file_path must stay within the workspace: %q", rawPath)
	}
	return target, filepath.ToSlash(relativePath), nil
}

func repairableEditToolResult(call llms.ToolCall, response repairableEditResponse) llms.ToolResult {
	encoded, _ := json.Marshal(response)
	return llms.ToolResult{
		ToolCallID: call.ID,
		Name:       "edit",
		Content:    string(encoded),
		Value:      response,
	}
}

func newGoValidateTool(workspace, stage string) core.Tool {
	toolName := "go_validate_" + stage
	tool := core.NewTool(&llms.FunctionDefinition{
		Name:        toolName,
		Description: fmt.Sprintf("Formats and validates the generated Go module at the fixed %s stage. Compiler and test failures are returned as repairable tool output.", stage),
		Parameters: state.JSONSchema{
			"type":                 "object",
			"additionalProperties": false,
		},
		OutputSchema: state.JSONSchema{"type": "object"},
	}, func(_ context.Context, call llms.ToolCall) (llms.ToolResult, error) {
		if err := core.DecodeToolArguments(call, &struct{}{}); err != nil {
			return llms.ToolResult{}, err
		}
		content, passed := validateGeneratedStage(workspace, stage)
		content = summarizeGeneratedOutput(content)
		finalAnswer := ""
		if passed {
			marker := map[string]string{
				"frontend": "FRONTEND_READY",
				"source":   "SOURCE_VERIFIED",
				"package":  "PACKAGE_VERIFIED",
			}[stage]
			if marker != "" {
				finalAnswer = content + "\n" + marker
			}
		}
		return llms.ToolResult{
			ToolCallID:  call.ID,
			Name:        toolName,
			Content:     content,
			Value:       map[string]any{"stage": stage, "passed": passed, "output": content},
			FinalAnswer: finalAnswer,
		}, nil
	})
	tool.Permissions = []string{"process.execute"}
	tool.Approval = core.ToolApprovalRequired
	tool.Effect = core.EffectNonIdempotentWrite
	return tool
}

func validateGeneratedStage(workspace, stage string) (string, bool) {
	if content, passed := validateGeneratedModule(workspace); !passed {
		return content, false
	}
	rootFiles, err := filepath.Glob(filepath.Join(workspace, "*.go"))
	if err != nil {
		return fmt.Sprintf("validation failed: discover Go files: %v", err), false
	}
	goFiles := make([]string, 0, len(rootFiles)+1)
	for _, path := range rootFiles {
		if stage != "package" && strings.HasSuffix(path, "_test.go") {
			continue
		}
		goFiles = append(goFiles, filepath.Base(path))
	}
	sort.Strings(goFiles)
	switch stage {
	case "frontend", "source":
	case "package":
		goFiles = append(goFiles, filepath.Join("cmd", "tiny", "main.go"))
	default:
		return fmt.Sprintf("validation failed: unsupported stage %q", stage), false
	}
	if len(goFiles) == 0 {
		return "validation failed: no generated Go files found", false
	}
	if output, err := runGeneratedCommand(workspace, nil, "gofmt", append([]string{"-w"}, goFiles...)...); err != nil {
		return fmt.Sprintf("gofmt failed: %v\n%s", err, output), false
	}
	if output, err := runGeneratedCommand(workspace, nil, "go", "test", "./..."); err != nil {
		return fmt.Sprintf("go test failed: %v\n%s", err, compactGeneratedOutput(output)), false
	}
	if stage == "package" {
		acceptanceOutput, passed := validateGeneratedAcceptance(workspace)
		if !passed {
			return acceptanceOutput, false
		}
		return "validation passed: gofmt, go test ./..., and typed-language CLI acceptance\n" + acceptanceOutput, true
	}
	return "validation passed: gofmt and go test ./...", true
}

func validateGeneratedModule(workspace string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(workspace, "go.mod"))
	if err != nil {
		return fmt.Sprintf("validation failed: read go.mod: %v", err), false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "module ") {
			continue
		}
		if line != "module example.com/tiny" {
			return fmt.Sprintf("validation failed: go.mod must declare exactly module example.com/tiny, got %q", line), false
		}
		return "module validation passed", true
	}
	return "validation failed: go.mod has no module directive", false
}

type generatedAcceptanceCase struct {
	name       string
	source     string
	wantOutput string
	wantError  string
}

var generatedAcceptanceCases = []generatedAcceptanceCase{
	{
		name: "complete typed program",
		source: `fn add(a int, b int) -> int { return a + b; }
fn fact(n int) -> int { if n == 0 { return 1; } else { return n * fact(n - 1); } }
fn noop() {}
let operation: (int, int) -> int = add;
let total: int = 0;
let i: int = 0;
while i < 3 { total = total + add(i, 1); i = i + 1; }
if total == 6 && !(false || false) { print(toInt("42")); } else { print(0); }
print(fact(5));
print(len("abc"));
print(operation(20, 22));
let precedence: int = 2 + 3 * 4;
print(precedence);
if false { print(0); } else { print(7); }
noop();
let outer: int = 1;
{ let outer: int = 2; }
print(outer);`,
		wantOutput: "42\n120\n3\n42\n14\n7\n1",
	},
	{name: "undefined name", source: `print(missing);`, wantError: "undefined"},
	{name: "duplicate declaration", source: `let value: int = 1; let value: int = 2;`, wantError: "duplicate"},
	{name: "invalid assignment", source: `let value: int = 1; value = true;`, wantError: "assign"},
	{name: "bad argument count", source: `fn id(value int) -> int { return value; } print(id());`, wantError: "argument"},
	{name: "bad argument type", source: `fn id(value int) -> int { return value; } print(id(true));`, wantError: "argument"},
	{name: "invalid operator", source: `print(1 + true);`, wantError: "operator"},
	{name: "non-boolean condition", source: `if 1 { print(1); }`, wantError: "condition"},
	{name: "division by zero", source: `print(1 / 0);`, wantError: "division by zero"},
	{name: "invalid toInt", source: `print(toInt("not-an-int"));`, wantError: "toInt"},
	{name: "loop limit", source: `while true {}`, wantError: "loop limit"},
	{name: "malformed input without panic", source: `let = ;`, wantError: "error"},
}

func validateGeneratedAcceptance(workspace string) (string, bool) {
	passed := make([]string, 0, len(generatedAcceptanceCases)+1)
	for _, test := range generatedAcceptanceCases {
		output, err := runGeneratedCommand(workspace, strings.NewReader(test.source+"\n"), "go", "run", "./cmd/tiny")
		if strings.Contains(strings.ToLower(output), "panic:") {
			return fmt.Sprintf("typed-language acceptance %q panicked:\n%s", test.name, output), false
		}
		if test.wantError != "" {
			if err == nil {
				return fmt.Sprintf("typed-language acceptance %q expected an error, got output %q", test.name, strings.TrimSpace(output)), false
			}
			if !strings.Contains(strings.ToLower(output), strings.ToLower(test.wantError)) {
				return fmt.Sprintf("typed-language acceptance %q error did not contain %q:\n%s", test.name, test.wantError, output), false
			}
		} else {
			if err != nil {
				return fmt.Sprintf("typed-language acceptance %q failed: %v\n%s", test.name, err, output), false
			}
			if strings.TrimSpace(output) != test.wantOutput {
				return fmt.Sprintf("typed-language acceptance %q output = %q, want %q", test.name, strings.TrimSpace(output), test.wantOutput), false
			}
		}
		passed = append(passed, test.name)
	}

	examplePath := filepath.Join(workspace, ".supervisor-acceptance.tiny")
	if err := os.WriteFile(examplePath, []byte(generatedAcceptanceCases[0].source+"\n"), 0o644); err != nil {
		return fmt.Sprintf("typed-language file acceptance setup failed: %v", err), false
	}
	defer os.Remove(examplePath)
	output, err := runGeneratedCommand(workspace, nil, "go", "run", "./cmd/tiny", filepath.Base(examplePath))
	if err != nil {
		return fmt.Sprintf("typed-language file acceptance failed: %v\n%s", err, output), false
	}
	if strings.TrimSpace(output) != generatedAcceptanceCases[0].wantOutput {
		return fmt.Sprintf("typed-language file acceptance output = %q, want %q", strings.TrimSpace(output), generatedAcceptanceCases[0].wantOutput), false
	}
	passed = append(passed, "CLI file input")
	exampleOutput, examplePassed := validateGeneratedExample(workspace)
	if !examplePassed {
		return exampleOutput, false
	}
	passed = append(passed, "generated example script")
	return "typed-language acceptance passed: " + strings.Join(passed, ", "), true
}

func validateGeneratedExample(workspace string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(workspace, "example.tiny"))
	if err != nil {
		return fmt.Sprintf("generated example validation failed: read example.tiny: %v", err), false
	}
	source := string(data)
	for _, fragment := range []string{"fn ", "let ", "while ", "if ", "print("} {
		if !strings.Contains(source, fragment) {
			return fmt.Sprintf("generated example validation failed: example.tiny must demonstrate %q", fragment), false
		}
	}
	output, err := runGeneratedCommand(workspace, nil, "go", "run", "./cmd/tiny", "example.tiny")
	if err != nil {
		return fmt.Sprintf("generated example validation failed: %v\n%s", err, output), false
	}
	if strings.TrimSpace(output) == "" {
		return "generated example validation failed: example.tiny produced no output", false
	}
	return "generated example passed with output:\n" + output, true
}

func verifyAndBundleWorkspace(workspace string) (string, error) {
	rootFiles, err := filepath.Glob(filepath.Join(workspace, "*.go"))
	if err != nil {
		return "", fmt.Errorf("discover generated Go files: %w", err)
	}
	sort.Strings(rootFiles)
	files := []string{"go.mod"}
	sourceCount := 0
	testCount := 0
	for _, path := range rootFiles {
		name := filepath.Base(path)
		files = append(files, name)
		if strings.HasSuffix(name, "_test.go") {
			testCount++
		} else {
			sourceCount++
		}
	}
	if sourceCount == 0 || testCount == 0 {
		return "", fmt.Errorf("generated module requires source and test Go files, got %d source and %d test", sourceCount, testCount)
	}
	files = append(files, filepath.Join("cmd", "tiny", "main.go"), "example.tiny")
	contents := make(map[string]string, len(files))
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(workspace, name))
		if err != nil {
			return "", fmt.Errorf("read generated %s: %w", name, err)
		}
		contents[name] = string(data)
	}
	if !strings.Contains(contents["go.mod"], "module example.com/tiny") {
		return "", fmt.Errorf("generated go.mod must declare module example.com/tiny")
	}

	goFiles := make([]string, 0, len(files))
	for _, name := range files {
		if strings.HasSuffix(name, ".go") {
			goFiles = append(goFiles, name)
		}
	}
	formatOutput, err := runGeneratedCommand(workspace, nil, "gofmt", append([]string{"-l"}, goFiles...)...)
	if err != nil {
		return "", fmt.Errorf("check generated formatting: %w", err)
	}
	if strings.TrimSpace(formatOutput) != "" {
		return "", fmt.Errorf("generated files are not gofmt-clean: %s", strings.TrimSpace(formatOutput))
	}
	if output, err := runGeneratedCommand(workspace, nil, "go", "test", "./..."); err != nil {
		return "", fmt.Errorf("test generated module: %w\n%s", err, output)
	}
	runOutput, err := runGeneratedCommand(workspace, strings.NewReader("print(42);\n"), "go", "run", "./cmd/tiny")
	if err != nil {
		return "", fmt.Errorf("run generated example: %w\n%s", err, runOutput)
	}
	if !strings.Contains(runOutput, "42") {
		return "", fmt.Errorf("generated example output %q does not contain 42", strings.TrimSpace(runOutput))
	}
	exampleOutput, examplePassed := validateGeneratedExample(workspace)
	if !examplePassed {
		return "", errors.New(exampleOutput)
	}

	var result strings.Builder
	for _, name := range files {
		language := "go"
		if name == "go.mod" {
			language = ""
		} else if strings.HasSuffix(name, ".tiny") {
			language = "tiny"
		}
		_, _ = fmt.Fprintf(&result, "## %s\n\n```%s\n%s", filepath.ToSlash(name), language, contents[name])
		if !strings.HasSuffix(contents[name], "\n") {
			result.WriteByte('\n')
		}
		result.WriteString("```\n\n")
	}
	result.WriteString("## Commands\n\n```bash\ngofmt -w *.go cmd/tiny/main.go\ngo test ./...\ngo run ./cmd/tiny example.tiny\ngo run ./cmd/tiny <<< 'print(42);'\n```\n\n")
	_, _ = fmt.Fprintf(&result, "Verified bundled example output:\n\n```text\n%s```\n\nVerified stdin output:\n\n```text\n%s```\n\nEND_OF_DELIVERABLE", exampleOutput, runOutput)
	return result.String(), nil
}

func runGeneratedCommand(workspace string, input *strings.Reader, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = workspace
	command.Env = generatedCommandEnvironment()
	if input != nil {
		command.Stdin = input
	}
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	if ctx.Err() != nil {
		return output.String(), ctx.Err()
	}
	return output.String(), err
}

func generatedCommandEnvironment() []string {
	environment := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(name)
		if strings.Contains(upper, "KEY") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "CREDENTIAL") {
			continue
		}
		environment = append(environment, entry)
	}
	return environment
}

func compactGeneratedOutput(output string) string {
	const limit = 12000
	if len(output) <= limit {
		return output
	}
	const marker = "\n... output truncated ...\n"
	remaining := limit - len(marker)
	front := remaining * 2 / 3
	back := remaining - front
	return output[:front] + marker + output[len(output)-back:]
}

func summarizeGeneratedOutput(output string) string {
	const maxLines = 120
	lines := strings.Split(output, "\n")
	if len(lines) <= maxLines {
		return compactGeneratedOutput(output)
	}
	const headLines = 80
	const tailLines = maxLines - headLines
	selected := make([]string, 0, maxLines+1)
	selected = append(selected, lines[:headLines]...)
	selected = append(selected, "... validation output lines omitted ...")
	selected = append(selected, lines[len(lines)-tailLines:]...)
	return compactGeneratedOutput(strings.Join(selected, "\n"))
}

type retryingModel struct {
	model    llms.Model
	attempts int
	timeout  time.Duration
	delay    time.Duration
	maxDelay time.Duration
}

func (model retryingModel) Generate(ctx context.Context, request llms.ModelRequest) (*llms.ModelResponse, error) {
	if model.model == nil {
		return nil, errors.New("model is required")
	}
	attempts := max(model.attempts, 1)
	delay := max(model.delay, 0)
	var response *llms.ModelResponse
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		attemptCtx := ctx
		cancel := func() {}
		if model.timeout > 0 {
			attemptCtx, cancel = context.WithTimeout(ctx, model.timeout)
		}
		response, err = model.model.Generate(attemptCtx, request)
		cancel()
		if err == nil {
			return response, nil
		}
		if !core.IsRetryableErrorClass(core.ClassifyError(err)) {
			return response, err
		}
		if attempt == attempts {
			break
		}
		wait := delay
		var executionErr core.ExecutionError
		if errors.As(err, &executionErr) && executionErr.RetryAfter() > wait {
			wait = executionErr.RetryAfter()
		}
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return response, ctx.Err()
		}
		if delay > 0 {
			delay *= 2
			if model.maxDelay > 0 {
				delay = min(delay, model.maxDelay)
			}
		}
	}
	return response, err
}

func modelFromEnvironment() (*openai.LLM, error) {
	modelID := strings.TrimSpace(os.Getenv("OPENAI_MODEL"))
	provider := providerForModel(modelID)
	options := []openai.Option{
		openai.WithToken(strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))),
		openai.WithBaseURL(normalizeOpenAIBaseURL(os.Getenv("OPENAI_BASE_URL"))),
		openai.WithModel(modelID),
		openai.WithProvider(provider),
	}
	if provider == openai.ProviderDeepSeek {
		options = append(options, openai.WithExtraBody(map[string]any{"enable_thinking": false}))
	}
	return openai.New(options...)
}

func providerForModel(modelID string) openai.Provider {
	normalized := strings.ToLower(modelID)
	if strings.Contains(normalized, "deepseek") {
		return openai.ProviderDeepSeek
	}
	if strings.Contains(normalized, "qwen") {
		return openai.ProviderVLLM
	}
	return openai.ProviderOpenAI
}

func normalizeOpenAIBaseURL(raw string) string {
	baseURL := strings.TrimSpace(raw)
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return baseURL
	}
	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = "/v1"
	}
	return strings.TrimRight(parsed.String(), "/")
}

func newSupervisorGraph() (*wfgraph.Graph, error) {
	definition, err := dsl.DeserializeGraphDefinition(supervisorGraphJSON)
	if err != nil {
		return nil, fmt.Errorf("decode embedded supervisor graph: %w", err)
	}
	return wfgraph.NewBuilder(builtin.NewDefaultRegistry()).Build(definition, nil)
}

func decodeSupervisorHistory(raw any) []supervisorcap.Turn {
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var history []supervisorcap.Turn
	if err := json.Unmarshal(data, &history); err != nil {
		return nil
	}
	return history
}

func must(err error) {
	if err != nil {
		for current := err; current != nil; current = errors.Unwrap(current) {
			if classified, ok := current.(core.ExecutionError); ok {
				panic(fmt.Errorf("%s (class=%s details=%v): %w", current, classified.Class(), classified.Details(), current))
			}
		}
		panic(err)
	}
}
