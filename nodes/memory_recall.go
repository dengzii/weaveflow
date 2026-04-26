package nodes

import (
	"context"
	"errors"
	"strings"
	"time"
	"weaveflow/dsl"
	"weaveflow/memory"
	fruntime "weaveflow/runtime"

	"github.com/google/uuid"
)

const defaultMemoryStatePath = fruntime.StateKeyMemory

type MemoryRecallNode struct {
	NodeInfo
	manager                memory.Manager
	StateScope             string
	MemoryStatePath        string
	QueryPath              string
	RequestInputPath       string
	OrchestrationStatePath string
	Limit                  int
	Roles                  []string
	Tags                   []string
	Types                  []memory.EntryType
}

func NewMemoryRecallNode(manager memory.Manager) *MemoryRecallNode {
	id := uuid.New()
	return &MemoryRecallNode{
		NodeInfo: NodeInfo{
			NodeID:          "MemoryRecall_" + id.String(),
			NodeName:        "MemoryRecall",
			NodeDescription: "Recall long-term memory into structured state for downstream planning and execution.",
		},
		manager:                manager,
		RequestInputPath:       fruntime.StateKeyRequest + ".input",
		OrchestrationStatePath: fruntime.StateKeyOrchestration,
	}
}

func (n *MemoryRecallNode) Invoke(ctx context.Context, state fruntime.State) (fruntime.State, error) {
	if state == nil {
		state = fruntime.State{}
	}

	memoryState, err := ensureObjectStateAtPath(state, n.effectiveMemoryStatePath())
	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "memory.recall.error", map[string]any{"error": err.Error()})
		return state, err
	}

	queryText, shouldRecall, querySource := n.resolveQuery(state)
	query := &memory.Query{
		Text:  queryText,
		Roles: cloneMemoryStrings(n.Roles),
		Tags:  cloneMemoryStrings(n.Tags),
		Types: cloneMemoryTypes(n.Types),
		Limit: n.Limit,
	}

	var recalled []memory.Entry
	if shouldRecall && n.manager != nil {
		recalled, err = n.manager.Recall(query)
		if err != nil {
			_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "memory.recall.error", map[string]any{
				"error": err.Error(),
				"query": serializeMemoryQuery(query, querySource, shouldRecall),
			})
			return state, err
		}
	}

	memoryState["query"] = serializeMemoryQuery(query, querySource, shouldRecall)
	memoryState["recalled"] = serializeMemoryEntries(recalled)
	memoryState["stats"] = map[string]any{
		"backend_enabled": n.manager != nil,
		"requested":       shouldRecall,
		"count":           len(recalled),
		"source":          querySource,
	}

	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "memory.recall", map[string]any{
		"memory_state_path": n.effectiveMemoryStatePath(),
		"query":             memoryState["query"],
		"stats":             memoryState["stats"],
		"recalled":          memoryState["recalled"],
	})

	return state, nil
}

func (n *MemoryRecallNode) GraphNodeSpec() dsl.GraphNodeSpec {
	config := map[string]any{
		"memory_state_path":        n.effectiveMemoryStatePath(),
		"request_input_path":       n.effectiveRequestInputPath(),
		"orchestration_state_path": n.effectiveOrchestrationStatePath(),
	}
	if stateScope := strings.TrimSpace(n.StateScope); stateScope != "" {
		config["state_scope"] = stateScope
	}
	if queryPath := strings.TrimSpace(n.QueryPath); queryPath != "" {
		config["query_path"] = queryPath
	}
	if n.Limit > 0 {
		config["limit"] = n.Limit
	}
	if len(n.Roles) > 0 {
		config["roles"] = cloneMemoryStrings(n.Roles)
	}
	if len(n.Tags) > 0 {
		config["tags"] = cloneMemoryStrings(n.Tags)
	}
	if len(n.Types) > 0 {
		config["types"] = serializeMemoryTypes(n.Types)
	}

	return dsl.GraphNodeSpec{
		ID:          n.ID(),
		Name:        n.Name(),
		Type:        "memory_recall",
		Description: n.Description(),
		Config:      config,
	}
}

func (n *MemoryRecallNode) resolveQuery(state fruntime.State) (string, bool, string) {
	if queryPath := strings.TrimSpace(n.QueryPath); queryPath != "" {
		if value, ok := fruntime.ResolveStatePath(state, queryPath); ok {
			text := strings.TrimSpace(stringifyStateValue(value))
			if text != "" {
				return text, true, queryPath
			}
		}
	}

	useMemory := false
	if value, ok := fruntime.ResolveStatePath(state, n.effectiveOrchestrationStatePath()+".use_memory"); ok {
		if parsed, parsedOK := boolStateValue(value); parsedOK {
			useMemory = parsed
		}
		if !useMemory {
			return "", false, n.effectiveOrchestrationStatePath() + ".use_memory"
		}
	}

	if value, ok := fruntime.ResolveStatePath(state, n.effectiveOrchestrationStatePath()+".memory_query"); ok {
		text := strings.TrimSpace(stringifyStateValue(value))
		if text != "" {
			return text, true, n.effectiveOrchestrationStatePath() + ".memory_query"
		}
	}

	if value, ok := fruntime.ResolveStatePath(state, n.effectiveRequestInputPath()); ok {
		text := strings.TrimSpace(stringifyStateValue(value))
		if text != "" {
			return text, true, n.effectiveRequestInputPath()
		}
	}

	conversation := fruntime.Conversation(state, "")
	for _, scope := range []string{n.StateScope, ""} {
		if scope != "" {
			conversation = fruntime.Conversation(state, scope)
		}
		messages := conversation.Messages()
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role != "human" {
				continue
			}
			text := strings.TrimSpace(extractText(messages[i]))
			if text != "" {
				return text, true, "conversation.last_human_message"
			}
		}
	}

	return "", false, ""
}

func (n *MemoryRecallNode) effectiveMemoryStatePath() string {
	if n == nil || strings.TrimSpace(n.MemoryStatePath) == "" {
		return defaultMemoryStatePath
	}
	return strings.TrimSpace(n.MemoryStatePath)
}

func (n *MemoryRecallNode) effectiveRequestInputPath() string {
	if n == nil || strings.TrimSpace(n.RequestInputPath) == "" {
		return fruntime.StateKeyRequest + ".input"
	}
	return strings.TrimSpace(n.RequestInputPath)
}

func (n *MemoryRecallNode) effectiveOrchestrationStatePath() string {
	if n == nil || strings.TrimSpace(n.OrchestrationStatePath) == "" {
		return fruntime.StateKeyOrchestration
	}
	return strings.TrimSpace(n.OrchestrationStatePath)
}

func serializeMemoryEntries(entries []memory.Entry) []map[string]any {
	if len(entries) == 0 {
		return []map[string]any{}
	}

	items := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		items = append(items, map[string]any{
			"id":         entry.ID,
			"text":       entry.Text,
			"role":       entry.Role,
			"payload":    cloneMemoryMap(entry.Payload),
			"created_at": formatTimeValue(entry.CreatedAt),
			"type":       string(entry.Type),
			"tags":       cloneMemoryStrings(entry.Tags),
		})
	}
	return items
}

func serializeMemoryQuery(query *memory.Query, source string, requested bool) map[string]any {
	if query == nil {
		return map[string]any{
			"requested": false,
			"source":    strings.TrimSpace(source),
		}
	}
	return map[string]any{
		"text":      strings.TrimSpace(query.Text),
		"roles":     cloneMemoryStrings(query.Roles),
		"tags":      cloneMemoryStrings(query.Tags),
		"types":     serializeMemoryTypes(query.Types),
		"since":     formatTimeValue(query.Since),
		"until":     formatTimeValue(query.Until),
		"limit":     query.Limit,
		"source":    strings.TrimSpace(source),
		"requested": requested,
	}
}

func formatTimeValue(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func cloneMemoryMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func cloneMemoryStrings(input []string) []string {
	if len(input) == 0 {
		return nil
	}
	cloned := make([]string, len(input))
	copy(cloned, input)
	return cloned
}

func cloneMemoryTypes(input []memory.EntryType) []memory.EntryType {
	if len(input) == 0 {
		return nil
	}
	cloned := make([]memory.EntryType, len(input))
	copy(cloned, input)
	return cloned
}

func serializeMemoryTypes(input []memory.EntryType) []string {
	if len(input) == 0 {
		return nil
	}
	items := make([]string, 0, len(input))
	for _, entryType := range input {
		text := strings.TrimSpace(string(entryType))
		if text != "" {
			items = append(items, text)
		}
	}
	return items
}

func MemoryEntriesFromState(value any) ([]memory.Entry, error) {
	switch typed := value.(type) {
	case nil:
		return []memory.Entry{}, nil
	case []memory.Entry:
		cloned := make([]memory.Entry, len(typed))
		copy(cloned, typed)
		return cloned, nil
	case []map[string]any:
		return deserializeMemoryEntries(typed)
	case []any:
		items := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			typedItem, ok := item.(map[string]any)
			if !ok {
				return nil, errors.New("memory entries must be objects")
			}
			items = append(items, typedItem)
		}
		return deserializeMemoryEntries(items)
	default:
		return nil, errors.New("memory entries must be an array")
	}
}

func deserializeMemoryEntries(items []map[string]any) ([]memory.Entry, error) {
	if len(items) == 0 {
		return []memory.Entry{}, nil
	}

	entries := make([]memory.Entry, 0, len(items))
	for _, item := range items {
		entry := memory.Entry{
			ID:   strings.TrimSpace(stringifyStateValue(item["id"])),
			Text: strings.TrimSpace(stringifyStateValue(item["text"])),
			Role: strings.TrimSpace(stringifyStateValue(item["role"])),
			Type: memory.EntryType(strings.TrimSpace(stringifyStateValue(item["type"]))),
			Tags: cloneMemoryStrings(anyToMemoryStrings(item["tags"])),
		}
		if payload, ok := item["payload"].(map[string]any); ok {
			entry.Payload = cloneMemoryMap(payload)
		}
		if createdAt, ok := item["created_at"]; ok {
			if typedTime, timeOK := createdAt.(interface{ IsZero() bool }); timeOK {
				_ = typedTime
			}
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func anyToMemoryStrings(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			text := strings.TrimSpace(stringifyStateValue(item))
			if text != "" {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}
