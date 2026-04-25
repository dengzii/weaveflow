package runtime

import (
	"encoding/json"
	"testing"
)

func TestValidateNodeContractNoViolations(t *testing.T) {
	t.Parallel()

	contract := NodeWriteContract{
		WritePaths: []string{"shared.messages", "shared.topic"},
	}
	changes := []StateChange{
		{Path: "shared.messages", Before: json.RawMessage(`[]`), After: json.RawMessage(`[{"role":"user"}]`)},
		{Path: "shared.topic", Before: json.RawMessage(`"old"`), After: json.RawMessage(`"new"`)},
	}

	violations := ValidateNodeContract("llm", contract, State{}, changes)
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %+v", violations)
	}
}

func TestValidateNodeContractUndeclaredWrite(t *testing.T) {
	t.Parallel()

	contract := NodeWriteContract{
		WritePaths: []string{"shared.messages"},
	}
	changes := []StateChange{
		{Path: "shared.messages", After: json.RawMessage(`[{"role":"user"}]`)},
		{Path: "shared.secret", After: json.RawMessage(`"leaked"`)},
	}

	violations := ValidateNodeContract("bad_node", contract, State{}, changes)
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", len(violations), violations)
	}
	if violations[0].Kind != "undeclared_write" {
		t.Fatalf("expected undeclared_write, got %q", violations[0].Kind)
	}
	if violations[0].Path != "shared.secret" {
		t.Fatalf("expected path shared.secret, got %q", violations[0].Path)
	}
}

func TestValidateNodeContractWildcardSkips(t *testing.T) {
	t.Parallel()

	contract := NodeWriteContract{Wildcard: true}
	changes := []StateChange{
		{Path: "shared.anything", After: json.RawMessage(`"value"`)},
	}

	violations := ValidateNodeContract("subgraph", contract, State{}, changes)
	if len(violations) != 0 {
		t.Fatalf("expected wildcard to skip validation, got %+v", violations)
	}
}

func TestValidateNodeContractEmptyContractSkips(t *testing.T) {
	t.Parallel()

	contract := NodeWriteContract{}
	changes := []StateChange{
		{Path: "shared.anything", After: json.RawMessage(`"value"`)},
	}

	violations := ValidateNodeContract("node", contract, State{}, changes)
	if len(violations) != 0 {
		t.Fatalf("expected empty contract to skip validation, got %+v", violations)
	}
}

func TestValidateNodeContractMissingRequired(t *testing.T) {
	t.Parallel()

	contract := NodeWriteContract{
		WritePaths:    []string{"shared.final_answer"},
		RequiredPaths: []string{"shared.final_answer"},
	}
	afterState := State{}

	violations := ValidateNodeContract("llm", contract, afterState, nil)
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", len(violations), violations)
	}
	if violations[0].Kind != "missing_required" {
		t.Fatalf("expected missing_required, got %q", violations[0].Kind)
	}
}

func TestValidateNodeContractNestedPathMatch(t *testing.T) {
	t.Parallel()

	contract := NodeWriteContract{
		WritePaths: []string{"shared.planner"},
	}
	changes := []StateChange{
		{Path: "shared.planner.status", After: json.RawMessage(`"done"`)},
		{Path: "shared.planner.steps", After: json.RawMessage(`[]`)},
	}

	violations := ValidateNodeContract("planner_node", contract, State{}, changes)
	if len(violations) != 0 {
		t.Fatalf("expected nested paths under declared parent to be allowed, got %+v", violations)
	}
}

func TestValidateNodeContractParentWriteMatchesChildDeclaration(t *testing.T) {
	t.Parallel()

	contract := NodeWriteContract{
		WritePaths: []string{"shared.planner.status", "shared.planner.steps"},
	}
	changes := []StateChange{
		{Path: "shared.planner", After: json.RawMessage(`{"status":"done","steps":[]}`)},
	}

	violations := ValidateNodeContract("planner_node", contract, State{}, changes)
	if len(violations) != 0 {
		t.Fatalf("expected parent write to be allowed when children are declared, got %+v", violations)
	}
}

func TestValidateNodeContractReadOnlyViolation(t *testing.T) {
	t.Parallel()

	contract := NodeWriteContract{
		WritePaths: []string{"shared.output"},
	}
	changes := []StateChange{
		{Path: "shared.input", Before: json.RawMessage(`"old"`), After: json.RawMessage(`"modified"`)},
		{Path: "shared.output", After: json.RawMessage(`"result"`)},
	}

	violations := ValidateNodeContract("node", contract, State{}, changes)
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation for read-only path write, got %d: %+v", len(violations), violations)
	}
	if violations[0].Path != "shared.input" {
		t.Fatalf("expected violation on shared.input, got %q", violations[0].Path)
	}
}

func TestValidateNodeContractIgnoresRuntimeAndConversationPaths(t *testing.T) {
	t.Parallel()

	contract := NodeWriteContract{
		WritePaths: []string{"shared.topic"},
	}
	changes := []StateChange{
		{Path: "runtime", After: json.RawMessage(`{"run_id":"abc"}`)},
		{Path: "conversation", After: json.RawMessage(`{"messages":[]}`)},
		{Path: "shared.topic", After: json.RawMessage(`"test"`)},
	}

	violations := ValidateNodeContract("node", contract, State{}, changes)
	if len(violations) != 0 {
		t.Fatalf("expected runtime/conversation paths to be ignored, got %+v", violations)
	}
}

func TestNormalizeContractPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"messages", "shared.messages"},
		{"topic", "shared.topic"},
		{"final_answer", "shared.final_answer"},
		{"scopes.agent.messages", "scopes.agent.messages"},
		{"scopes.planner.status", "scopes.planner.status"},
		{"__wf_iterator", "internal.__wf_iterator"},
		{"__wf_iterator.loop1", "internal.__wf_iterator.loop1"},
		{"*", "*"},
		{"", ""},
		{"runtime", "runtime"},
		{"conversation", "conversation"},
		{"conversation.messages", "conversation.messages"},
		{"artifacts", "artifacts"},
	}

	for _, tt := range tests {
		got := NormalizeContractPath(tt.input)
		if got != tt.expected {
			t.Errorf("NormalizeContractPath(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestIsPathCoveredByContract(t *testing.T) {
	t.Parallel()

	writePaths := []string{"shared.messages", "shared.planner"}

	tests := []struct {
		path     string
		expected bool
	}{
		{"shared.messages", true},
		{"shared.planner", true},
		{"shared.planner.status", true},
		{"shared.planner.steps", true},
		{"shared.topic", false},
		{"shared.secret", false},
		{"shared.message", false},
	}

	for _, tt := range tests {
		got := isPathCoveredByContract(tt.path, writePaths)
		if got != tt.expected {
			t.Errorf("isPathCoveredByContract(%q, ...) = %v, want %v", tt.path, got, tt.expected)
		}
	}
}
