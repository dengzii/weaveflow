package nodes

import (
	"context"
	"strings"
	"weaveflow/dsl"
	"weaveflow/memory"
	fruntime "weaveflow/runtime"

	"github.com/google/uuid"
)

type MemoryWriteNode struct {
	NodeInfo
	manager            memory.Manager
	MemoryStatePath    string
	StateScope         string
	RequestInputPath   string
	FinalAnswerPath    string
	PlannerStatePath   string
	IncludeRequest     bool
	IncludeFinalAnswer bool
	IncludeSummary     bool
	Deduplicate        bool
	MinRequestLength   int
	MinAnswerLength    int
	MinSummaryLength   int
}

func NewMemoryWriteNode(manager memory.Manager) *MemoryWriteNode {
	id := uuid.New()
	return &MemoryWriteNode{
		NodeInfo: NodeInfo{
			NodeID:          "MemoryWrite_" + id.String(),
			NodeName:        "MemoryWrite",
			NodeDescription: "Persist durable request and final-answer memory for future runs.",
		},
		manager:            manager,
		RequestInputPath:   fruntime.StateKeyRequest + ".input",
		PlannerStatePath:   fruntime.StateKeyPlanner,
		IncludeRequest:     true,
		IncludeFinalAnswer: true,
		IncludeSummary:     true,
		Deduplicate:        true,
	}
}

func (n *MemoryWriteNode) Invoke(ctx context.Context, state fruntime.State) (fruntime.State, error) {
	if state == nil {
		state = fruntime.State{}
	}

	memoryState, err := ensureObjectStateAtPath(state, n.effectiveMemoryStatePath())
	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "memory.write.error", map[string]any{"error": err.Error()})
		return state, err
	}

	candidates := n.collectEntries(state)
	existing := []memory.Entry{}
	if n.manager != nil && n.Deduplicate {
		existing, err = n.manager.Load(nil)
		if err != nil {
			_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "memory.write.error", map[string]any{"error": err.Error()})
			return state, err
		}
	}

	entries, stats := n.filterEntries(candidates, existing)
	if n.manager != nil && len(entries) > 0 {
		if err := n.manager.Append(entries...); err != nil {
			_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "memory.write.error", map[string]any{
				"error":   err.Error(),
				"entries": serializeMemoryEntries(entries),
			})
			return state, err
		}
	}

	memoryState["written"] = serializeMemoryEntries(entries)
	memoryState["write_stats"] = map[string]any{
		"backend_enabled": n.manager != nil,
		"count":           len(entries),
		"candidate_count": len(candidates),
		"deduplicate":     n.Deduplicate,
		"min_request_len": n.MinRequestLength,
		"min_answer_len":  n.MinAnswerLength,
		"min_summary_len": n.MinSummaryLength,
		"skipped":         stats,
	}

	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "memory.write", map[string]any{
		"memory_state_path": n.effectiveMemoryStatePath(),
		"written":           memoryState["written"],
		"write_stats":       memoryState["write_stats"],
	})

	return state, nil
}

func (n *MemoryWriteNode) GraphNodeSpec() dsl.GraphNodeSpec {
	config := map[string]any{
		"memory_state_path":    n.effectiveMemoryStatePath(),
		"request_input_path":   n.effectiveRequestInputPath(),
		"include_request":      n.IncludeRequest,
		"include_final_answer": n.IncludeFinalAnswer,
		"include_summary":      n.IncludeSummary,
		"deduplicate":          n.Deduplicate,
	}
	if stateScope := strings.TrimSpace(n.StateScope); stateScope != "" {
		config["state_scope"] = stateScope
	}
	if finalAnswerPath := n.effectiveFinalAnswerPath(); finalAnswerPath != "" {
		config["final_answer_path"] = finalAnswerPath
	}
	if plannerStatePath := n.effectivePlannerStatePath(); plannerStatePath != "" {
		config["planner_state_path"] = plannerStatePath
	}
	if n.MinRequestLength > 0 {
		config["min_request_length"] = n.MinRequestLength
	}
	if n.MinAnswerLength > 0 {
		config["min_answer_length"] = n.MinAnswerLength
	}
	if n.MinSummaryLength > 0 {
		config["min_summary_length"] = n.MinSummaryLength
	}

	return dsl.GraphNodeSpec{
		ID:          n.ID(),
		Name:        n.Name(),
		Type:        "memory_write",
		Description: n.Description(),
		Config:      config,
	}
}

func (n *MemoryWriteNode) collectEntries(state fruntime.State) []memory.Entry {
	entries := make([]memory.Entry, 0, 3)

	if n.IncludeRequest {
		if value, ok := fruntime.ResolveStatePath(state, n.effectiveRequestInputPath()); ok {
			if text := strings.TrimSpace(stringifyStateValue(value)); text != "" {
				entries = append(entries, memory.Entry{
					Text: text,
					Role: "user",
					Type: memory.EntryTypeMessage,
					Tags: []string{"request", "user_input"},
				})
			}
		}
	}

	if n.IncludeFinalAnswer {
		text := n.resolveFinalAnswer(state)
		if text != "" {
			entries = append(entries, memory.Entry{
				Text: text,
				Role: "assistant",
				Type: memory.EntryTypeSummary,
				Tags: []string{"final_answer", "assistant_output"},
			})
		}
	}

	if n.IncludeSummary {
		text := n.resolveSummary(state)
		if text != "" {
			entries = append(entries, memory.Entry{
				Text: text,
				Role: "assistant",
				Type: memory.EntryTypeSummary,
				Tags: []string{"run_summary", "planner_summary", "assistant_output"},
			})
		}
	}

	return entries
}

func (n *MemoryWriteNode) filterEntries(candidates []memory.Entry, existing []memory.Entry) ([]memory.Entry, map[string]any) {
	if len(candidates) == 0 {
		return []memory.Entry{}, map[string]any{
			"short":      0,
			"duplicate":  0,
			"empty":      0,
			"candidates": 0,
		}
	}

	seen := map[string]struct{}{}
	if n.Deduplicate {
		for _, entry := range existing {
			seen[memoryWriteDedupKey(entry)] = struct{}{}
		}
	}

	filtered := make([]memory.Entry, 0, len(candidates))
	shortCount := 0
	duplicateCount := 0
	emptyCount := 0

	for _, entry := range candidates {
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			emptyCount++
			continue
		}
		if n.isTooShort(entry) {
			shortCount++
			continue
		}

		key := memoryWriteDedupKey(entry)
		if n.Deduplicate {
			if _, exists := seen[key]; exists {
				duplicateCount++
				continue
			}
			seen[key] = struct{}{}
		}

		filtered = append(filtered, entry)
	}

	return filtered, map[string]any{
		"short":      shortCount,
		"duplicate":  duplicateCount,
		"empty":      emptyCount,
		"candidates": len(candidates),
	}
}

func (n *MemoryWriteNode) isTooShort(entry memory.Entry) bool {
	textLen := len([]rune(strings.TrimSpace(entry.Text)))
	switch {
	case entry.Role == "user" && n.MinRequestLength > 0:
		return textLen < n.MinRequestLength
	case memoryEntryHasTag(entry, "run_summary") && n.MinSummaryLength > 0:
		return textLen < n.MinSummaryLength
	case entry.Role == "assistant" && n.MinAnswerLength > 0:
		return textLen < n.MinAnswerLength
	default:
		return false
	}
}

func (n *MemoryWriteNode) effectiveMemoryStatePath() string {
	if n == nil || strings.TrimSpace(n.MemoryStatePath) == "" {
		return defaultMemoryStatePath
	}
	return strings.TrimSpace(n.MemoryStatePath)
}

func (n *MemoryWriteNode) effectiveRequestInputPath() string {
	if n == nil || strings.TrimSpace(n.RequestInputPath) == "" {
		return fruntime.StateKeyRequest + ".input"
	}
	return strings.TrimSpace(n.RequestInputPath)
}

func (n *MemoryWriteNode) effectiveFinalAnswerPath() string {
	if n != nil && strings.TrimSpace(n.FinalAnswerPath) != "" {
		return strings.TrimSpace(n.FinalAnswerPath)
	}
	return ""
}

func (n *MemoryWriteNode) effectivePlannerStatePath() string {
	if n == nil || strings.TrimSpace(n.PlannerStatePath) == "" {
		return fruntime.StateKeyPlanner
	}
	return strings.TrimSpace(n.PlannerStatePath)
}

func (n *MemoryWriteNode) resolveFinalAnswer(state fruntime.State) string {
	if finalAnswerPath := n.effectiveFinalAnswerPath(); finalAnswerPath != "" {
		if value, ok := fruntime.ResolveStatePath(state, finalAnswerPath); ok {
			return strings.TrimSpace(stringifyStateValue(value))
		}
	}
	return strings.TrimSpace(fruntime.Conversation(state, n.StateScope).FinalAnswer())
}

func (n *MemoryWriteNode) resolveSummary(state fruntime.State) string {
	plannerSummary := ""
	currentStepID := ""
	if plannerValue, ok := fruntime.ResolveStatePath(state, n.effectivePlannerStatePath()); ok {
		switch typed := plannerValue.(type) {
		case fruntime.State:
			plannerSummary = strings.TrimSpace(stringifyStateValue(typed["summary"]))
			currentStepID = strings.TrimSpace(stringifyStateValue(typed["current_step_id"]))
		case map[string]any:
			plannerSummary = strings.TrimSpace(stringifyStateValue(typed["summary"]))
			currentStepID = strings.TrimSpace(stringifyStateValue(typed["current_step_id"]))
		}
	}
	finalAnswer := n.resolveFinalAnswer(state)
	if plannerSummary == "" && finalAnswer == "" {
		return ""
	}

	parts := make([]string, 0, 3)
	if plannerSummary != "" {
		parts = append(parts, "Plan summary: "+plannerSummary)
	}
	if currentStepID != "" {
		parts = append(parts, "Current step: "+currentStepID)
	}
	if finalAnswer != "" {
		parts = append(parts, "Outcome: "+finalAnswer)
	}
	return strings.Join(parts, "\n")
}

func memoryWriteDedupKey(entry memory.Entry) string {
	text := strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(entry.Text))), " ")
	return strings.TrimSpace(entry.Role) + "|" + string(entry.Type) + "|" + text
}

func memoryEntryHasTag(entry memory.Entry, target string) bool {
	target = strings.TrimSpace(target)
	for _, tag := range entry.Tags {
		if strings.TrimSpace(tag) == target {
			return true
		}
	}
	return false
}
