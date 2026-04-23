package nodes

import (
	"context"
	"fmt"
	"strings"
	"weaveflow/dsl"
	"weaveflow/memory"
	fruntime "weaveflow/runtime"

	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
)

const (
	defaultContextAssemblerHeading             = "Relevant recalled memory:"
	defaultContextAssemblerOrchestrationHeader = "Current orchestration state:"
	defaultContextAssemblerPlannerHeader       = "Current plan state:"
)

type ContextAssemblerNode struct {
	NodeInfo
	StateScope             string
	MemoryStatePath        string
	OrchestrationStatePath string
	PlannerStatePath       string
	IncludeMemory          bool
	IncludeOrchestration   bool
	IncludePlanner         bool
	MemoryHeading          string
	OrchestrationHeading   string
	PlannerHeading         string
}

func NewContextAssemblerNode() *ContextAssemblerNode {
	id := uuid.New()
	return &ContextAssemblerNode{
		NodeInfo: NodeInfo{
			NodeID:          "ContextAssembler_" + id.String(),
			NodeName:        "ContextAssembler",
			NodeDescription: "Assemble recalled memory into the active conversation context for the next model turn.",
		},
		MemoryStatePath:        defaultMemoryStatePath,
		OrchestrationStatePath: fruntime.StateKeyOrchestration,
		PlannerStatePath:       fruntime.StateKeyPlanner,
		IncludeMemory:          true,
		IncludeOrchestration:   true,
		IncludePlanner:         true,
		MemoryHeading:          defaultContextAssemblerHeading,
		OrchestrationHeading:   defaultContextAssemblerOrchestrationHeader,
		PlannerHeading:         defaultContextAssemblerPlannerHeader,
	}
}

func (n *ContextAssemblerNode) Invoke(ctx context.Context, state fruntime.State) (fruntime.State, error) {
	if state == nil {
		state = fruntime.State{}
	}

	conversation := fruntime.Conversation(state, n.StateScope)
	messages := conversation.Messages()
	if len(messages) == 0 {
		return state, nil
	}

	cleaned := removeContextAssemblerMessages(messages, n.contextAssemblerHeadings())
	injectedKinds := make([]string, 0, 3)
	if n.IncludeMemory {
		recalledValue, ok := fruntime.ResolveStatePath(state, n.effectiveMemoryStatePath()+".recalled")
		if ok {
			entries, err := MemoryEntriesFromState(recalledValue)
			if err != nil {
				_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "context.assembler.error", map[string]any{"error": err.Error()})
				return state, err
			}
			memoryMessage := n.buildMemoryContextMessage(entries)
			if memoryMessage != nil {
				cleaned = insertAfterLeadingSystem(cleaned, *memoryMessage)
				injectedKinds = append(injectedKinds, "memory")
			}
		}
	}
	if n.IncludeOrchestration {
		orchestrationMessage := n.buildOrchestrationContextMessage(state)
		if orchestrationMessage != nil {
			cleaned = insertAfterLeadingSystem(cleaned, *orchestrationMessage)
			injectedKinds = append(injectedKinds, "orchestration")
		}
	}
	if n.IncludePlanner {
		plannerMessage := n.buildPlannerContextMessage(state)
		if plannerMessage != nil {
			cleaned = insertAfterLeadingSystem(cleaned, *plannerMessage)
			injectedKinds = append(injectedKinds, "planner")
		}
	}

	conversation.UpdateMessage(cleaned)
	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "context.assembled", map[string]any{
		"state_scope":           strings.TrimSpace(n.StateScope),
		"memory_state_path":     n.effectiveMemoryStatePath(),
		"orchestration_path":    n.effectiveOrchestrationStatePath(),
		"planner_path":          n.effectivePlannerStatePath(),
		"include_memory":        n.IncludeMemory,
		"include_orchestration": n.IncludeOrchestration,
		"include_planner":       n.IncludePlanner,
		"injected_sections":     injectedKinds,
		"message_count":         len(cleaned),
	})

	return state, nil
}

func (n *ContextAssemblerNode) GraphNodeSpec() dsl.GraphNodeSpec {
	config := map[string]any{
		"memory_state_path":        n.effectiveMemoryStatePath(),
		"orchestration_state_path": n.effectiveOrchestrationStatePath(),
		"planner_state_path":       n.effectivePlannerStatePath(),
		"include_memory":           n.IncludeMemory,
		"include_orchestration":    n.IncludeOrchestration,
		"include_planner":          n.IncludePlanner,
		"memory_heading":           n.effectiveMemoryHeading(),
		"orchestration_heading":    n.effectiveOrchestrationHeading(),
		"planner_heading":          n.effectivePlannerHeading(),
	}
	if scope := strings.TrimSpace(n.StateScope); scope != "" {
		config["state_scope"] = scope
	}
	return dsl.GraphNodeSpec{
		ID:          n.ID(),
		Name:        n.Name(),
		Type:        "context_assembler",
		Description: n.Description(),
		Config:      config,
	}
}

func (n *ContextAssemblerNode) effectiveMemoryStatePath() string {
	if n == nil || strings.TrimSpace(n.MemoryStatePath) == "" {
		return defaultMemoryStatePath
	}
	return strings.TrimSpace(n.MemoryStatePath)
}

func (n *ContextAssemblerNode) effectiveMemoryHeading() string {
	if n == nil || strings.TrimSpace(n.MemoryHeading) == "" {
		return defaultContextAssemblerHeading
	}
	return strings.TrimSpace(n.MemoryHeading)
}

func (n *ContextAssemblerNode) effectiveOrchestrationStatePath() string {
	if n == nil || strings.TrimSpace(n.OrchestrationStatePath) == "" {
		return fruntime.StateKeyOrchestration
	}
	return strings.TrimSpace(n.OrchestrationStatePath)
}

func (n *ContextAssemblerNode) effectivePlannerStatePath() string {
	if n == nil || strings.TrimSpace(n.PlannerStatePath) == "" {
		return fruntime.StateKeyPlanner
	}
	return strings.TrimSpace(n.PlannerStatePath)
}

func (n *ContextAssemblerNode) effectiveOrchestrationHeading() string {
	if n == nil || strings.TrimSpace(n.OrchestrationHeading) == "" {
		return defaultContextAssemblerOrchestrationHeader
	}
	return strings.TrimSpace(n.OrchestrationHeading)
}

func (n *ContextAssemblerNode) effectivePlannerHeading() string {
	if n == nil || strings.TrimSpace(n.PlannerHeading) == "" {
		return defaultContextAssemblerPlannerHeader
	}
	return strings.TrimSpace(n.PlannerHeading)
}

func (n *ContextAssemblerNode) buildMemoryContextMessage(entries []memory.Entry) *llms.MessageContent {
	if len(entries) == 0 {
		return nil
	}

	lines := make([]string, 0, len(entries)+1)
	lines = append(lines, n.effectiveMemoryHeading())
	for _, entry := range entries {
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			continue
		}
		line := fmt.Sprintf("- [%s/%s] %s", strings.TrimSpace(entry.Role), strings.TrimSpace(string(entry.Type)), text)
		lines = append(lines, line)
	}
	if len(lines) <= 1 {
		return nil
	}
	message := llms.TextParts(llms.ChatMessageTypeSystem, strings.Join(lines, "\n"))
	return &message
}

func (n *ContextAssemblerNode) buildOrchestrationContextMessage(state fruntime.State) *llms.MessageContent {
	payload, ok := fruntime.ResolveStatePath(state, n.effectiveOrchestrationStatePath())
	if !ok {
		return nil
	}
	object := contextAssemblerObject(payload)
	if len(object) == 0 {
		return nil
	}

	lines := []string{n.effectiveOrchestrationHeading()}
	appendContextLine(&lines, "mode", object["mode"])
	appendContextLine(&lines, "use_memory", object["use_memory"])
	appendContextLine(&lines, "memory_query", object["memory_query"])
	appendContextLine(&lines, "needs_clarification", object["needs_clarification"])
	appendContextLine(&lines, "clarification_question", object["clarification_question"])
	appendContextLine(&lines, "target_subgraph", object["target_subgraph"])
	if len(lines) == 1 {
		return nil
	}
	message := llms.TextParts(llms.ChatMessageTypeSystem, strings.Join(lines, "\n"))
	return &message
}

func (n *ContextAssemblerNode) buildPlannerContextMessage(state fruntime.State) *llms.MessageContent {
	payload, ok := fruntime.ResolveStatePath(state, n.effectivePlannerStatePath())
	if !ok {
		return nil
	}
	object := contextAssemblerObject(payload)
	if len(object) == 0 {
		return nil
	}

	lines := []string{n.effectivePlannerHeading()}
	appendContextLine(&lines, "objective", object["objective"])
	appendContextLine(&lines, "status", object["status"])
	appendContextLine(&lines, "current_step_id", object["current_step_id"])
	appendContextLine(&lines, "summary", object["summary"])
	appendPlannerSteps(&lines, object["plan"])
	if len(lines) == 1 {
		return nil
	}
	message := llms.TextParts(llms.ChatMessageTypeSystem, strings.Join(lines, "\n"))
	return &message
}

func (n *ContextAssemblerNode) contextAssemblerHeadings() []string {
	headings := make([]string, 0, 3)
	if n.IncludeMemory {
		headings = append(headings, n.effectiveMemoryHeading())
	}
	if n.IncludeOrchestration {
		headings = append(headings, n.effectiveOrchestrationHeading())
	}
	if n.IncludePlanner {
		headings = append(headings, n.effectivePlannerHeading())
	}
	return headings
}

func removeContextAssemblerMessages(messages []llms.MessageContent, headings []string) []llms.MessageContent {
	if len(messages) == 0 {
		return nil
	}
	result := make([]llms.MessageContent, 0, len(messages))
	for _, message := range messages {
		if message.Role == llms.ChatMessageTypeSystem && hasContextAssemblerHeading(strings.TrimSpace(extractText(message)), headings) {
			continue
		}
		result = append(result, message)
	}
	return result
}

func hasContextAssemblerHeading(text string, headings []string) bool {
	for _, heading := range headings {
		if heading != "" && strings.HasPrefix(text, heading) {
			return true
		}
	}
	return false
}

func contextAssemblerObject(value any) map[string]any {
	switch typed := value.(type) {
	case fruntime.State:
		return typed
	case map[string]any:
		return typed
	default:
		return nil
	}
}

func appendContextLine(lines *[]string, key string, value any) {
	text := strings.TrimSpace(stringifyStateValue(value))
	if text == "" || text == "{}" || text == "null" || text == "[]" {
		return
	}
	*lines = append(*lines, fmt.Sprintf("- %s: %s", key, text))
}

func appendPlannerSteps(lines *[]string, value any) {
	steps := contextAssemblerPlanSteps(value)
	if len(steps) == 0 {
		return
	}
	*lines = append(*lines, "- next_steps:")
	limit := min(3, len(steps))
	for i := 0; i < limit; i++ {
		step := steps[i]
		title := strings.TrimSpace(stringifyStateValue(step["title"]))
		if title == "" {
			title = strings.TrimSpace(stringifyStateValue(step["id"]))
		}
		status := strings.TrimSpace(stringifyStateValue(step["status"]))
		kind := strings.TrimSpace(stringifyStateValue(step["kind"]))
		summary := title
		meta := make([]string, 0, 2)
		if status != "" {
			meta = append(meta, status)
		}
		if kind != "" {
			meta = append(meta, kind)
		}
		if len(meta) > 0 {
			summary += " [" + strings.Join(meta, ", ") + "]"
		}
		*lines = append(*lines, "  - "+summary)
	}
}

func contextAssemblerPlanSteps(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return typed
	case []any:
		items := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if object, ok := item.(map[string]any); ok {
				items = append(items, object)
			}
		}
		return items
	default:
		return nil
	}
}

func insertAfterLeadingSystem(messages []llms.MessageContent, injected llms.MessageContent) []llms.MessageContent {
	index := 0
	for index < len(messages) && messages[index].Role == llms.ChatMessageTypeSystem {
		index++
	}
	result := make([]llms.MessageContent, 0, len(messages)+1)
	result = append(result, messages[:index]...)
	result = append(result, injected)
	result = append(result, messages[index:]...)
	return result
}
