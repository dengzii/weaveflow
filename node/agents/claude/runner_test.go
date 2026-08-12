package claude

import (
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestClaudeArgumentsUseNonInteractiveRestrictedProtocol(t *testing.T) {
	config := resolvedClaudeRun{
		resolvedClaudeRunConfig: resolvedClaudeRunConfig{
			resolvedClaudeRunnerConfig: resolvedClaudeRunnerConfig{RunnerConfig: RunnerConfig{
				Access:          WorkspaceAccessReadOnly,
				Model:           "claude-sonnet-4-6",
				MaxBudgetUSD:    2.5,
				Tools:           []string{"Read", "Glob", "Grep"},
				AllowedTools:    []string{"Read", "Glob", "Grep"},
				DisallowedTools: []string{"Edit", "Write", "Bash"},
			}},
		},
	}
	got := claudeArguments(config)
	want := []string{
		"--bare",
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--permission-mode", "dontAsk",
		"--strict-mcp-config",
		"--disable-slash-commands",
		"--no-session-persistence",
		"--tools", "Read,Glob,Grep",
		"--allowedTools", "Read,Glob,Grep",
		"--disallowedTools", "Edit,Write,Bash",
		"--max-budget-usd", "2.5",
		"--model", "claude-sonnet-4-6",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments = %#v", got)
	}
}

func TestClaudeArgumentsEnableSandboxForWritableWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native Windows write policy is rejected before argument construction")
	}
	config := resolvedClaudeRun{
		resolvedClaudeRunConfig: resolvedClaudeRunConfig{
			resolvedClaudeRunnerConfig: resolvedClaudeRunnerConfig{RunnerConfig: RunnerConfig{
				Access: WorkspaceAccessWrite,
				Tools:  []string{"Read", "Edit", "Write"},
			}},
		},
	}
	arguments := strings.Join(claudeArguments(config), " ")
	if !strings.Contains(arguments, `--settings {"sandbox":{"enabled":true,"autoAllowBashIfSandboxed":false}}`) {
		t.Fatalf("arguments = %s", arguments)
	}
}

func TestValidateClaudeHelpRequiresUsedOptions(t *testing.T) {
	help := strings.Join([]string{
		"--bare", "--print", "--output-format", "--verbose", "--include-partial-messages",
		"--permission-mode", "--tools", "--allowedTools", "--disallowedTools", "--max-budget-usd",
		"--no-session-persistence", "--strict-mcp-config", "--disable-slash-commands", "--model", "--settings",
	}, " ")
	if err := validateClaudeHelp(help); err != nil {
		t.Fatal(err)
	}
	if err := validateClaudeHelp("--bare --print"); err == nil {
		t.Fatal("expected missing option error")
	}
}
