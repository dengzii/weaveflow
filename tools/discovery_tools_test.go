package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGlobToolFindsSortedFilesSkipsDependenciesAndTruncates(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "old.go", "package old\n")
	writeTestFile(t, root, "src/new.go", "package newest\n")
	writeTestFile(t, root, "src/notes.txt", "notes\n")
	writeTestFile(t, root, "node_modules/ignored.go", "package ignored\n")
	oldTime := time.Date(2026, 8, 25, 1, 0, 0, 0, time.UTC)
	newTime := oldTime.Add(time.Hour)
	if err := os.Chtimes(filepath.Join(root, "old.go"), oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes(old.go): %v", err)
	}
	if err := os.Chtimes(filepath.Join(root, "src/new.go"), newTime, newTime); err != nil {
		t.Fatalf("Chtimes(src/new.go): %v", err)
	}
	t.Setenv(toolWorkspaceEnv, root)

	result, err := globTool(context.Background(), toolCallForTest("glob", `{"pattern":"**/*.go"}`))
	if err != nil {
		t.Fatalf("globTool() error = %v", err)
	}
	response, ok := result.Value.(globResponse)
	if !ok {
		t.Fatalf("glob result = %#v", result.Value)
	}
	if response.Root != "." || response.Workspace != root || response.Truncated || response.Scanned != 3 {
		t.Fatalf("glob response metadata = %#v", response)
	}
	if len(response.Paths) != 2 || response.Paths[0].Path != "src/new.go" || response.Paths[1].Path != "old.go" {
		t.Fatalf("glob paths = %#v", response.Paths)
	}

	limited, err := globTool(context.Background(), toolCallForTest("glob", `{"pattern":"**/*.go","max_results":1}`))
	if err != nil {
		t.Fatalf("limited globTool() error = %v", err)
	}
	limitedResponse := limited.Value.(globResponse)
	if len(limitedResponse.Paths) != 1 || !limitedResponse.Truncated {
		t.Fatalf("limited glob response = %#v", limitedResponse)
	}
}

func TestGlobToolValidatesRequestRootAndLimits(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "content")
	t.Setenv(toolWorkspaceEnv, root)

	for _, arguments := range []string{`{}`, `{"pattern":"   "}`, `{"pattern":"*","path":"../outside"}`} {
		if _, err := globTool(context.Background(), toolCallForTest("glob", arguments)); err == nil {
			t.Fatalf("globTool(%s) accepted invalid input", arguments)
		}
	}
	if _, err := globTool(context.Background(), toolCallForTest("glob", `{"pattern":"*","path":"file.txt"}`)); err == nil || !strings.Contains(err.Error(), "root must be a directory") {
		t.Fatalf("glob file root error = %v", err)
	}
	for input, want := range map[int]int{-1: defaultGlobResults, 0: defaultGlobResults, 5: 5, maxGlobResults + 1: maxGlobResults} {
		if got := normalizeGlobLimit(input); got != want {
			t.Fatalf("normalizeGlobLimit(%d) = %d, want %d", input, got, want)
		}
	}
	for _, testCase := range []struct {
		pattern string
		path    string
		want    bool
	}{
		{pattern: "**/*.go", path: "main.go", want: true},
		{pattern: "src/**/test?.[jt]s", path: "src/unit/test1.ts", want: true},
		{pattern: "src/*.go", path: "src/nested/main.go", want: false},
		{pattern: "[", path: "file", want: false},
	} {
		if got := matchGlob(testCase.pattern, testCase.path); got != testCase.want {
			t.Fatalf("matchGlob(%q, %q) = %v, want %v", testCase.pattern, testCase.path, got, testCase.want)
		}
	}
}

func TestTreeToolRendersDirectoriesBeforeFilesAndHonorsBounds(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "z.txt", "z")
	writeTestFile(t, root, "alpha/a.txt", "a")
	writeTestFile(t, root, "alpha/deep/b.txt", "b")
	writeTestFile(t, root, "node_modules/pkg/ignored.js", "ignored")
	writeTestFile(t, root, ".hidden/secret.txt", "hidden")
	t.Setenv(toolWorkspaceEnv, root)

	result, err := treeTool(context.Background(), toolCallForTest("tree", `{"max_depth":2}`))
	if err != nil {
		t.Fatalf("treeTool() error = %v", err)
	}
	output := result.Content
	if !strings.HasPrefix(output, "./\n") || !strings.Contains(output, "alpha/\n  deep/\n  a.txt\n") || !strings.Contains(output, "z.txt\n") {
		t.Fatalf("tree output =\n%s", output)
	}
	if strings.Contains(output, "node_modules") || strings.Contains(output, ".hidden") || strings.Contains(output, "b.txt") {
		t.Fatalf("tree output ignored depth or skip rules:\n%s", output)
	}
	if strings.Index(output, "alpha/") > strings.Index(output, "z.txt") {
		t.Fatalf("tree did not render directories before files:\n%s", output)
	}

	limited, err := treeTool(context.Background(), toolCallForTest("tree", `{"max_entries":1}`))
	if err != nil {
		t.Fatalf("limited treeTool() error = %v", err)
	}
	if !strings.Contains(limited.Content, "[truncated: 1 entries cap reached") {
		t.Fatalf("limited tree output =\n%s", limited.Content)
	}
	if _, err := treeTool(context.Background(), toolCallForTest("tree", `{"path":"z.txt"}`)); err == nil || !strings.Contains(err.Error(), "must be a directory") {
		t.Fatalf("tree file path error = %v", err)
	}
	if _, err := treeTool(context.Background(), toolCallForTest("tree", `{"path":"../outside"}`)); err == nil {
		t.Fatal("tree accepted path outside workspace")
	}
	for input, want := range map[int]int{-1: defaultTreeMaxDepth, 0: defaultTreeMaxDepth, 2: 2, maxTreeMaxDepth + 1: maxTreeMaxDepth} {
		if got := normalizeTreeDepth(input); got != want {
			t.Fatalf("normalizeTreeDepth(%d) = %d, want %d", input, got, want)
		}
	}
	for input, want := range map[int]int{-1: defaultTreeMaxEntries, 0: defaultTreeMaxEntries, 2: 2, maxTreeMaxEntries + 1: maxTreeMaxEntries} {
		if got := normalizeTreeMaxEntries(input); got != want {
			t.Fatalf("normalizeTreeMaxEntries(%d) = %d, want %d", input, got, want)
		}
	}
}

func TestOutlineToolRendersGoAndPythonDeclarations(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "sample.go", `package sample

const Answer = 42
var Label string

type Box[T any] struct {
	Value T
	Embedded
}

type Runner interface {
	Run(...string) error
}

func Top(values map[string][]int, rest ...string) (chan int, error) { return nil, nil }
func (box *Box[T]) Method(input pkg.Type) *Box[T] { return box }
`)
	writeTestFile(t, root, "sample.py", `# ignored
class Worker:
    def run(self):
        pass

def top_level():
    pass
`)
	t.Setenv(toolWorkspaceEnv, root)

	flat, err := outlineTool(context.Background(), toolCallForTest("outline", `{"file_path":"sample.go"}`))
	if err != nil {
		t.Fatalf("Go outline error = %v", err)
	}
	for _, expected := range []string{"sample.go (Go,", "const Answer", "var Label", "struct Box { 2 fields }", "interface Runner { 1 methods }", "func Top(", "  func (*Box[T]) Method("} {
		if !strings.Contains(flat.Content, expected) {
			t.Fatalf("Go outline missing %q:\n%s", expected, flat.Content)
		}
	}

	grouped, err := outlineTool(context.Background(), toolCallForTest("outline", `{"file_path":"sample.go","grouped":true}`))
	if err != nil {
		t.Fatalf("grouped Go outline error = %v", err)
	}
	for _, heading := range []string{"# types", "# functions", "# vars", "# consts"} {
		if !strings.Contains(grouped.Content, heading) {
			t.Fatalf("grouped outline missing %q:\n%s", heading, grouped.Content)
		}
	}

	python, err := outlineTool(context.Background(), toolCallForTest("outline", `{"file_path":"sample.py"}`))
	if err != nil {
		t.Fatalf("Python outline error = %v", err)
	}
	if !strings.Contains(python.Content, "class Worker:") || !strings.Contains(python.Content, "  def run(self):") || !strings.Contains(python.Content, "def top_level():") {
		t.Fatalf("Python outline =\n%s", python.Content)
	}
}

func TestOutlineToolHandlesUnknownAndInvalidInputs(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "notes.unknown", "plain text\n")
	t.Setenv(toolWorkspaceEnv, root)

	result, err := outlineTool(context.Background(), toolCallForTest("outline", `{"file_path":"notes.unknown"}`))
	if err != nil {
		t.Fatalf("unknown outline error = %v", err)
	}
	if !strings.Contains(result.Content, "Unknown") || !strings.Contains(result.Content, "no top-level declarations") {
		t.Fatalf("unknown outline =\n%s", result.Content)
	}
	for _, arguments := range []string{`{}`, `{"file_path":"."}`, `{"file_path":"../outside"}`} {
		if _, err := outlineTool(context.Background(), toolCallForTest("outline", arguments)); err == nil {
			t.Fatalf("outlineTool(%s) accepted invalid input", arguments)
		}
	}
	if got := trimOutlineLine(strings.Repeat("x", outlineMaxLineLen+10)); len(got) <= outlineMaxLineLen || !strings.HasSuffix(got, "…") {
		t.Fatalf("trimOutlineLine() = %q", got)
	}
}

func writeTestFile(t *testing.T, root, relativePath, content string) {
	t.Helper()
	target := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", target, err)
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", target, err)
	}
}
