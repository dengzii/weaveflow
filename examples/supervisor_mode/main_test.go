package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/llms"
	"github.com/dengzii/weaveflow/llms/openai"
)

func TestNormalizeOpenAIBaseURL(t *testing.T) {
	tests := []struct {
		name string
		base string
		want string
	}{
		{name: "empty", base: "", want: ""},
		{name: "host root", base: "https://provider.example", want: "https://provider.example/v1"},
		{name: "host root slash", base: "https://provider.example/", want: "https://provider.example/v1"},
		{name: "existing v1", base: "https://provider.example/v1", want: "https://provider.example/v1"},
		{name: "custom path", base: "https://provider.example/openai/v1/", want: "https://provider.example/openai/v1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeOpenAIBaseURL(test.base); got != test.want {
				t.Fatalf("normalizeOpenAIBaseURL(%q) = %q, want %q", test.base, got, test.want)
			}
		})
	}
}

func TestProviderForModel(t *testing.T) {
	if got := providerForModel("DeepSeek-V4-Flash"); got != openai.ProviderDeepSeek {
		t.Fatalf("providerForModel(DeepSeek) = %q", got)
	}
	if got := providerForModel("gpt-5"); got != openai.ProviderOpenAI {
		t.Fatalf("providerForModel(gpt-5) = %q", got)
	}
	if got := providerForModel("Qwen3.8-27B"); got != openai.ProviderVLLM {
		t.Fatalf("providerForModel(Qwen) = %q", got)
	}
}

func TestRetryingModelRetriesOnlyTransientErrors(t *testing.T) {
	transient := &retryTestModel{failures: 1, class: core.ErrorUnavailable}
	model := retryingModel{model: transient, attempts: 2}
	if _, err := model.Generate(context.Background(), llms.ModelRequest{}); err != nil {
		t.Fatalf("Generate() transient error = %v", err)
	}
	if transient.calls != 2 {
		t.Fatalf("transient calls = %d, want 2", transient.calls)
	}

	nonRetryable := &retryTestModel{failures: 2, class: core.ErrorInvalidInput}
	model = retryingModel{model: nonRetryable, attempts: 3}
	if _, err := model.Generate(context.Background(), llms.ModelRequest{}); err == nil {
		t.Fatal("Generate() non-retryable error = nil")
	}
	if nonRetryable.calls != 1 {
		t.Fatalf("non-retryable calls = %d, want 1", nonRetryable.calls)
	}
}

func TestRetryingModelHonorsRetryAfter(t *testing.T) {
	transient := &retryAfterTestModel{delay: 10 * time.Millisecond}
	model := retryingModel{model: transient, attempts: 2}
	started := time.Now()
	if _, err := model.Generate(context.Background(), llms.ModelRequest{}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed < transient.delay {
		t.Fatalf("Generate() elapsed = %s, want at least %s", elapsed, transient.delay)
	}
}

type retryTestModel struct {
	calls    int
	failures int
	class    core.ErrorClass
}

type retryAfterTestModel struct {
	calls int
	delay time.Duration
}

func (model *retryAfterTestModel) Generate(context.Context, llms.ModelRequest) (*llms.ModelResponse, error) {
	model.calls++
	if model.calls == 1 {
		return nil, core.NewExecutionError(core.ErrorRateLimited, "slow down", nil, nil).WithRetryAfter(model.delay)
	}
	return &llms.ModelResponse{Choices: []*llms.ModelChoice{{Content: "ready"}}}, nil
}

func (model *retryTestModel) Generate(context.Context, llms.ModelRequest) (*llms.ModelResponse, error) {
	model.calls++
	if model.calls <= model.failures {
		return nil, core.NewExecutionError(model.class, "model failure", nil, nil)
	}
	return &llms.ModelResponse{Choices: []*llms.ModelChoice{{Content: "ready"}}}, nil
}

func TestSupervisorGraphBuilds(t *testing.T) {
	graph, err := newSupervisorGraph()
	if err != nil {
		t.Fatalf("newSupervisorGraph() error = %v", err)
	}
	if graph == nil {
		t.Fatal("newSupervisorGraph() returned nil")
	}
}

func TestVerifyAndBundleWorkspace(t *testing.T) {
	workspace := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/tiny\n\ngo 1.21\n",
		"tiny.go": `package tiny

import "io"

type Value struct{}
type Option func(*config)
type config struct{ output io.Writer }

func WithOutput(output io.Writer) Option { return func(config *config) { config.output = output } }
func Run(_ string, options ...Option) (Value, error) {
	configuration := config{output: io.Discard}
	for _, option := range options { option(&configuration) }
	_, _ = io.WriteString(configuration.output, "42\n")
	return Value{}, nil
}
`,
		"tiny_test.go": `package tiny

import "testing"

func TestRun(t *testing.T) {
	if _, err := Run("print(42);"); err != nil { t.Fatal(err) }
}
`,
		filepath.Join("cmd", "tiny", "main.go"): `package main

import (
	"os"
	"example.com/tiny"
)

func main() { _, _ = tiny.Run("", tiny.WithOutput(os.Stdout)) }
`,
		"example.tiny": `fn add(a int, b int) -> int { return a + b; }
let total: int = 0;
let i: int = 0;
while i < 2 { total = add(total, i); i = i + 1; }
if total == 1 { print(total); } else { print(0); }
`,
	}
	for name, content := range files {
		path := filepath.Join(workspace, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"tiny.go", "tiny_test.go", filepath.Join("cmd", "tiny", "main.go")} {
		if output, err := runGeneratedCommand(workspace, nil, "gofmt", "-w", name); err != nil {
			t.Fatalf("gofmt fixture: %v\n%s", err, output)
		}
	}
	bundle, err := verifyAndBundleWorkspace(workspace)
	if err != nil {
		t.Fatalf("verifyAndBundleWorkspace() error = %v", err)
	}
	for _, marker := range []string{"## go.mod", "## tiny.go", "## tiny_test.go", "## cmd/tiny/main.go", "## example.tiny", "Verified bundled example output", "END_OF_DELIVERABLE"} {
		if !strings.Contains(bundle, marker) {
			t.Fatalf("bundle missing %q", marker)
		}
	}
}

func TestGeneratedCommandEnvironmentRedactsCredentials(t *testing.T) {
	t.Setenv("SUPERVISOR_TEST_API_KEY", "secret")
	for _, entry := range generatedCommandEnvironment() {
		if strings.HasPrefix(entry, "SUPERVISOR_TEST_API_KEY=") {
			t.Fatalf("generated command environment contains credential: %q", entry)
		}
	}
}

func TestCompactGeneratedOutputPreservesFailureContext(t *testing.T) {
	short := "short output"
	if got := compactGeneratedOutput(short); got != short {
		t.Fatalf("compactGeneratedOutput(short) = %q, want %q", got, short)
	}
	large := strings.Repeat("a", 20000)
	got := compactGeneratedOutput(large)
	if len(got) > 12000 || !strings.Contains(got, "output truncated") {
		t.Fatalf("compactGeneratedOutput(large) length=%d", len(got))
	}
}

func TestSummarizeGeneratedOutputLimitsLineNoise(t *testing.T) {
	lines := make([]string, 200)
	for index := range lines {
		lines[index] = fmt.Sprintf("line-%d", index)
	}
	got := summarizeGeneratedOutput(strings.Join(lines, "\n"))
	if !strings.Contains(got, "line-0") || !strings.Contains(got, "line-199") || !strings.Contains(got, "omitted") {
		t.Fatalf("summarizeGeneratedOutput() lost context: %q", got)
	}
}

func TestValidateGeneratedStageReturnsRepairableFailure(t *testing.T) {
	content, passed := validateGeneratedStage(t.TempDir(), "source")
	if passed || !strings.Contains(content, "read go.mod") {
		t.Fatalf("validateGeneratedStage() = %q, %v", content, passed)
	}
}

func TestValidateGeneratedFrontendStage(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module example.com/tiny\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "tiny.go"), []byte("package tiny\n\nfunc Parse(string) error { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	content, passed := validateGeneratedStage(workspace, "frontend")
	if !passed || !strings.Contains(content, "validation passed") {
		t.Fatalf("validateGeneratedStage(frontend) = %q, %v", content, passed)
	}
}

func TestValidateGeneratedExampleRequiresFeatureCoverage(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "example.tiny"), []byte("print(1);\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	content, passed := validateGeneratedExample(workspace)
	if passed || !strings.Contains(content, `must demonstrate "fn "`) {
		t.Fatalf("validateGeneratedExample() = %q, %v", content, passed)
	}
}

func TestGoValidateToolIsBoundToConfiguredStage(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module example.com/tiny\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "tiny.go"), []byte("package tiny\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := newGoValidateTool(workspace, "source")
	if tool.Name() != "go_validate_source" {
		t.Fatalf("tool name = %q, want go_validate_source", tool.Name())
	}
	result, err := executeApprovedTool(tool, llms.ToolCall{
		ID:   "validate-source",
		Type: "function",
		FunctionCall: &llms.FunctionCall{
			Name:      tool.Name(),
			Arguments: []byte(`{}`),
		},
	}, "process.execute")
	if err != nil {
		t.Fatalf("source validation error = %v", err)
	}
	if !strings.Contains(result.FinalAnswer, "SOURCE_VERIFIED") || strings.Contains(result.FinalAnswer, "PACKAGE_VERIFIED") {
		t.Fatalf("source validation final answer = %q", result.FinalAnswer)
	}
	_, err = executeApprovedTool(tool, llms.ToolCall{
		ID:   "override-stage",
		Type: "function",
		FunctionCall: &llms.FunctionCall{
			Name:      tool.Name(),
			Arguments: []byte(`{"stage":"package"}`),
		},
	}, "process.execute")
	if err == nil {
		t.Fatal("stage-bound validation accepted a stage override")
	}
}

func TestValidateGeneratedModuleRequiresExactModule(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "go.mod")
	if err := os.WriteFile(path, []byte("module example.com/tinylang\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	content, passed := validateGeneratedModule(workspace)
	if passed || !strings.Contains(content, "exactly module example.com/tiny") {
		t.Fatalf("validateGeneratedModule(wrong) = %q, %v", content, passed)
	}
	if err := os.WriteFile(path, []byte("module example.com/tiny\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	content, passed = validateGeneratedModule(workspace)
	if !passed || !strings.Contains(content, "passed") {
		t.Fatalf("validateGeneratedModule(correct) = %q, %v", content, passed)
	}
}

func TestRepairableEditToolReturnsMatchFailures(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "notes.txt")
	if err := os.WriteFile(path, []byte("same same"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := newRepairableEditTool(workspace)
	for _, test := range []struct {
		name      string
		oldString string
		message   string
	}{
		{name: "empty old string", oldString: "", message: "old_string is required"},
		{name: "missing", oldString: "missing", message: "not found"},
		{name: "ambiguous", oldString: "same", message: "not unique"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := executeApprovedEditTool(tool, repairableEditCall(t, "notes.txt", test.oldString, "new", false))
			if err != nil {
				t.Fatalf("repairable edit returned hard error: %v", err)
			}
			response, ok := result.Value.(repairableEditResponse)
			if !ok {
				t.Fatalf("repairable edit result = %#v", result.Value)
			}
			if response.Passed || !strings.Contains(response.Message, test.message) {
				t.Fatalf("repairable edit response = %#v", response)
			}
		})
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "same same" {
		t.Fatalf("failed edits changed file to %q", data)
	}
}

func TestRepairableEditToolReplacesUniqueMatch(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "notes.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := newRepairableEditTool(workspace)
	result, err := executeApprovedEditTool(tool, repairableEditCall(t, "notes.txt", "world", "weaveflow", false))
	if err != nil {
		t.Fatalf("repairable edit returned error: %v", err)
	}
	response, ok := result.Value.(repairableEditResponse)
	if !ok || !response.Passed || response.Replacements != 1 {
		t.Fatalf("repairable edit response = %#v", result.Value)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello weaveflow\n" {
		t.Fatalf("edited file = %q", data)
	}
}

func TestRepairableEditToolRejectsUnsafePaths(t *testing.T) {
	workspace := t.TempDir()
	tool := newRepairableEditTool(workspace)
	for _, path := range []string{"../outside.txt", filepath.Join(string(os.PathSeparator), "tmp", "outside.txt")} {
		if _, err := executeApprovedEditTool(tool, repairableEditCall(t, path, "old", "new", false)); err == nil {
			t.Fatalf("repairable edit accepted unsafe path %q", path)
		}
	}
}

func repairableEditCall(t *testing.T, filePath, oldString, newString string, replaceAll bool) llms.ToolCall {
	t.Helper()
	arguments, err := json.Marshal(map[string]any{
		"file_path":   filePath,
		"old_string":  oldString,
		"new_string":  newString,
		"replace_all": replaceAll,
	})
	if err != nil {
		t.Fatal(err)
	}
	return llms.ToolCall{
		ID:   "edit-call",
		Type: "function",
		FunctionCall: &llms.FunctionCall{
			Name:      "edit",
			Arguments: arguments,
		},
	}
}

func executeApprovedEditTool(tool core.Tool, call llms.ToolCall) (llms.ToolResult, error) {
	return executeApprovedTool(tool, call, "filesystem.write")
}

func executeApprovedTool(tool core.Tool, call llms.ToolCall, permissions ...string) (llms.ToolResult, error) {
	ctx := core.WithToolPermissions(context.Background(), permissions...)
	ctx = core.WithToolApprover(ctx, core.ToolApproverFunc(func(context.Context, core.ToolApprovalRequest) (core.ToolApprovalDecision, error) {
		return core.ToolApprovalDecision{Approved: true}, nil
	}))
	return core.ExecuteTool(ctx, tool, call)
}
