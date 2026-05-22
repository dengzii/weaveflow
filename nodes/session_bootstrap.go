package nodes

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"weaveflow/dsl"
	fruntime "weaveflow/runtime"
	wfstate "weaveflow/state"

	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
)

const defaultSessionBootstrapInputPath = wfstate.StateKeyRequest + ".input"

// inputSource identifies which resolution rule produced the bootstrap input.
// Surfaced in observability events so prompt-debugging can tell config inputs
// apart from values that originated outside this node.
type inputSource string

const (
	inputSourceEmpty       inputSource = "empty"
	inputSourceConfig      inputSource = "config_input"
	inputSourceInputPath   inputSource = "input_path"
	inputSourceRequest     inputSource = "request_state"
	inputSourceLastMessage inputSource = "last_human_message"
)

// templateVarRE matches {{ path.with.dots }} placeholders. Whitespace inside
// the braces is allowed. The captured group is a dot-separated state path
// suitable for state.ResolvePath.
var templateVarRE = regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_.]*)\s*\}\}`)

// SessionBootstrapNode prepares the minimum session state required before an
// agent/executor graph starts doing real work.
//
// Its responsibilities are intentionally narrow and deterministic:
//  1. Resolve the request input text.
//     Resolution order is:
//     - n.Input when explicitly configured
//     - n.InputPath when configured
//     - request.input in runtime state
//     - the latest human message already present in the scoped conversation
//  2. Seed or enrich top-level runtime state buckets that later nodes expect:
//     - request
//     - agent
//     - tool_policy
//  3. Initialize the scoped conversation if it is still empty.
//     When no messages exist yet, it can prepend a system prompt and append the
//     resolved human input as the initial message list.
//  4. Normalize conversation execution limits by setting max iterations on the
//     scoped conversation state.
//
// Important behavioral details:
//   - It does not clear or rebuild an existing conversation. If messages are
//     already present, it only ensures the configured system prompt is present
//     at the front; user/assistant history is otherwise preserved.
//   - Metadata/profile/tool-policy values are merged into state rather than
//     replacing the whole target object.
//   - It emits a runner context event and a best-effort debug artifact so replay
//     and troubleshooting can inspect the exact bootstrap result.
//
// In short, this node is the boundary between an external request and a graph's
// internal agent session model: after it runs, downstream nodes can assume the
// scoped conversation and core request/agent/tool state already exist.
type SessionBootstrapNode struct {
	NodeInfo
	StateScope      string
	Input           string
	InputPath       string
	SystemPrompt    string
	MaxIterations   int
	AgentProfile    map[string]any
	RequestMetadata map[string]any
	ToolPolicy      map[string]any

	// NowFunc returns the wall-clock time injected into request.metadata.now.
	// Tests may override it for deterministic output. Defaults to time.Now.
	NowFunc func() time.Time
}

// NewSessionBootstrapNode creates a bootstrap node with a unique runtime node
// identity and the default max-iteration budget, leaving all request/session
// specifics to be supplied by graph configuration.
func NewSessionBootstrapNode() *SessionBootstrapNode {
	id := uuid.New()
	return &SessionBootstrapNode{
		NodeInfo: NodeInfo{
			NodeID:          "SessionBootstrap_" + id.String(),
			NodeName:        "SessionBootstrap",
			NodeDescription: "Initialize request, agent, tool policy, and scoped conversation state for an agent run.",
		},
		MaxIterations: wfstate.DefaultMaxIterations,
	}
}

func (n *SessionBootstrapNode) execute(ctx context.Context, state wfstate.State) (wfstate.State, error) {
	if state == nil {
		state = wfstate.State{}
	}

	input, source, err := n.resolveInput(state)
	if err != nil {
		if fruntime.HasArtifactRecorder(ctx) {
			_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "session.bootstrap.error", map[string]any{"error": err.Error()})
		}
		return state, err
	}

	// Populate state buckets first so templates below can reference any
	// metadata, profile, or runtime field via {{ request.metadata.now }} etc.
	request := state.Ensure(wfstate.StateKeyRequest)
	mergeBootstrapMap(request, "metadata", n.RequestMetadata)
	n.injectRuntimeMetadata(ctx, request)

	agent := state.Ensure(wfstate.StateKeyAgent)
	mergeBootstrapMap(agent, "profile", n.AgentProfile)

	toolPolicy := state.Ensure(wfstate.StateKeyToolPolicy)
	mergeBootstrapValues(toolPolicy, n.ToolPolicy)

	n.injectBudgetDeadline(ctx, state)

	// Only config-driven Input is treated as a template. Values resolved from
	// state (input_path / request / conversation) are user-authored content
	// and must not be re-interpolated, otherwise a user typing "{{x}}" would
	// trigger lookups against state.
	var unresolved []string
	if source == inputSourceConfig {
		rendered, missing := renderTemplate(input, state)
		input = rendered
		unresolved = appendUnique(unresolved, missing)
	}

	// Resume semantics: only overwrite request.input when this turn actually
	// resolved a non-empty input. Otherwise a no-input resume would clobber a
	// prior turn's value. The first non-empty input also seeds original_input,
	// which is then frozen so downstream nodes can always reference the very
	// first user ask, no matter how many bootstrap cycles run.
	requestInputPreserved := false
	originalInputSet := false
	if input != "" {
		request["input"] = input
		if existing, _ := request["original_input"].(string); existing == "" {
			request["original_input"] = input
			originalInputSet = true
		}
	} else if existing, _ := request["input"].(string); existing != "" {
		requestInputPreserved = true
	}

	systemPrompt := strings.TrimSpace(n.SystemPrompt)
	if systemPrompt != "" {
		rendered, missing := renderTemplate(systemPrompt, state)
		systemPrompt = rendered
		unresolved = appendUnique(unresolved, missing)
	}

	conversation := state.Conversation(n.StateScope)
	messages := conversation.Messages()
	hadExistingMessages := len(messages) > 0
	systemPromptChanged := false
	if len(messages) == 0 {
		messages = n.initialMessages(systemPrompt, input)
		if len(messages) > 0 {
			conversation.UpdateMessage(messages)
		}
	} else if updated, changed := n.ensureSystemPrompt(messages, systemPrompt); changed {
		conversation.UpdateMessage(updated)
		systemPromptChanged = true
	}
	conversation.SetMaxIterations(n.effectiveMaxIterations())

	event := map[string]any{
		"kind":                    "session_bootstrap",
		"state_scope":             strings.TrimSpace(n.StateScope),
		"input_source":            string(source),
		"input_chars":             len(input),
		"has_input":               strings.TrimSpace(input) != "",
		"request_input_preserved": requestInputPreserved,
		"original_input_set":      originalInputSet,
		"system_prompt_chars":     len(systemPrompt),
		"had_system_prompt":       systemPrompt != "",
		"system_prompt_changed":   systemPromptChanged,
		"had_existing_messages":   hadExistingMessages,
		"message_count":           len(conversation.Messages()),
		"max_iterations":          n.effectiveMaxIterations(),
	}
	if md, ok := fruntime.RunnerMetadataFromContext(ctx); ok && md.RunID != "" {
		event["run_id"] = md.RunID
	}
	if len(unresolved) > 0 {
		event["unresolved_template_keys"] = unresolved
	}
	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, event)

	if fruntime.HasArtifactRecorder(ctx) {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "session.bootstrap", n.artifactPayload(state, input))
	}

	return state, nil
}

func appendUnique(dst, src []string) []string {
	if len(src) == 0 {
		return dst
	}
	seen := make(map[string]struct{}, len(dst))
	for _, key := range dst {
		seen[key] = struct{}{}
	}
	for _, key := range src {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		dst = append(dst, key)
	}
	return dst
}

// injectRuntimeMetadata pulls runtime-only fields (run_id, now) into
// request.metadata. Values already provided by config take precedence so
// callers can override for replay or deterministic test scenarios.
func (n *SessionBootstrapNode) injectRuntimeMetadata(ctx context.Context, request wfstate.State) {
	if request == nil {
		return
	}
	metadata, _ := request["metadata"].(map[string]any)
	if metadata == nil {
		if typed, ok := request["metadata"].(wfstate.State); ok {
			metadata = typed
		}
	}
	if metadata == nil {
		metadata = map[string]any{}
		request["metadata"] = metadata
	}

	if _, set := metadata["run_id"]; !set {
		if md, ok := fruntime.RunnerMetadataFromContext(ctx); ok && md.RunID != "" {
			metadata["run_id"] = md.RunID
		}
	}
	if _, set := metadata["now"]; !set {
		metadata["now"] = n.now().Format(time.RFC3339)
	}
}

func (n *SessionBootstrapNode) now() time.Time {
	if n != nil && n.NowFunc != nil {
		return n.NowFunc()
	}
	return time.Now()
}

// injectBudgetDeadline snapshots the context deadline into state.budget so
// downstream planner/tool nodes can shape behavior against a wall-clock cutoff.
// Other budget fields (usage, limits, status, ...) are untouched. Existing
// deadline values win so explicit configuration is never overwritten.
func (n *SessionBootstrapNode) injectBudgetDeadline(ctx context.Context, state wfstate.State) {
	if ctx == nil {
		return
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return
	}

	budget := state.Ensure(wfstate.StateKeyBudget)
	if _, set := budget["deadline"]; !set {
		budget["deadline"] = deadline.Format(time.RFC3339)
	}
	if _, set := budget["remaining_seconds"]; !set {
		budget["remaining_seconds"] = max(int(deadline.Sub(n.now()).Seconds()), 0)
	}
}

func (n *SessionBootstrapNode) Execute(ctx context.Context, input wfstate.State) (wfstate.StatePatch, error) {
	return executeStatePatch(input, func(state wfstate.State) (wfstate.State, error) {
		return n.execute(ctx, state)
	})
}

func (n *SessionBootstrapNode) GraphNodeSpec() dsl.GraphNodeSpec {
	config := map[string]any{
		"state_scope":    n.StateScope,
		"max_iterations": n.effectiveMaxIterations(),
	}
	if input := strings.TrimSpace(n.Input); input != "" {
		config["input"] = input
	}
	if inputPath := strings.TrimSpace(n.InputPath); inputPath != "" {
		config["input_path"] = inputPath
	}
	if systemPrompt := strings.TrimSpace(n.SystemPrompt); systemPrompt != "" {
		config["system_prompt"] = systemPrompt
	}
	if len(n.AgentProfile) > 0 {
		config["agent_profile"] = wfstate.CloneMap(n.AgentProfile)
	}
	if len(n.RequestMetadata) > 0 {
		config["request_metadata"] = wfstate.CloneMap(n.RequestMetadata)
	}
	if len(n.ToolPolicy) > 0 {
		config["tool_policy"] = wfstate.CloneMap(n.ToolPolicy)
	}

	return dsl.GraphNodeSpec{
		ID:          n.ID(),
		Name:        n.Name(),
		Type:        "session_bootstrap",
		Description: n.Description(),
		Config:      config,
	}
}

func (n *SessionBootstrapNode) resolveInput(state wfstate.State) (string, inputSource, error) {
	if input := strings.TrimSpace(n.Input); input != "" {
		return input, inputSourceConfig, nil
	}

	if inputPath := strings.TrimSpace(n.InputPath); inputPath != "" {
		value, ok := state.ResolvePath(inputPath)
		if !ok {
			return "", inputSourceEmpty, fmt.Errorf("session bootstrap input not found at %q", inputPath)
		}
		return strings.TrimSpace(stringifyStateValue(value)), inputSourceInputPath, nil
	}

	if value, ok := state.ResolvePath(defaultSessionBootstrapInputPath); ok {
		if text := strings.TrimSpace(stringifyStateValue(value)); text != "" {
			return text, inputSourceRequest, nil
		}
	}

	messages := state.Conversation(n.StateScope).Messages()
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != llms.ChatMessageTypeHuman {
			continue
		}
		if text := strings.TrimSpace(extractText(messages[i])); text != "" {
			return text, inputSourceLastMessage, nil
		}
	}

	return "", inputSourceEmpty, nil
}

func (n *SessionBootstrapNode) initialMessages(systemPrompt, input string) []llms.MessageContent {
	messages := make([]llms.MessageContent, 0, 2)
	if systemPrompt = strings.TrimSpace(systemPrompt); systemPrompt != "" {
		messages = append(messages, llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt))
	}
	if input = strings.TrimSpace(input); input != "" {
		messages = append(messages, llms.TextParts(llms.ChatMessageTypeHuman, input))
	}
	return messages
}

func (n *SessionBootstrapNode) ensureSystemPrompt(messages []llms.MessageContent, systemPrompt string) ([]llms.MessageContent, bool) {
	systemPrompt = strings.TrimSpace(systemPrompt)
	if systemPrompt == "" || len(messages) == 0 {
		return messages, false
	}

	for _, message := range messages {
		if message.Role != llms.ChatMessageTypeSystem {
			break
		}
		if strings.TrimSpace(extractText(message)) == systemPrompt {
			return messages, false
		}
	}

	updated := make([]llms.MessageContent, 0, len(messages)+1)
	updated = append(updated, llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt))
	updated = append(updated, messages...)
	return updated, true
}

// renderTemplate substitutes {{ path }} placeholders in text by resolving each
// path against state. Placeholders whose path does not resolve are left intact
// and returned in the unresolved slice so observability can surface them.
func renderTemplate(text string, state wfstate.State) (string, []string) {
	if text == "" || !strings.Contains(text, "{{") {
		return text, nil
	}
	missing := map[string]struct{}{}
	rendered := templateVarRE.ReplaceAllStringFunc(text, func(match string) string {
		sub := templateVarRE.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		path := sub[1]
		value, ok := state.ResolvePath(path)
		if !ok {
			missing[path] = struct{}{}
			return match
		}
		return stringifyStateValue(value)
	})
	if len(missing) == 0 {
		return rendered, nil
	}
	keys := make([]string, 0, len(missing))
	for key := range missing {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return rendered, keys
}

func (n *SessionBootstrapNode) effectiveMaxIterations() int {
	if n == nil || n.MaxIterations <= 0 {
		return wfstate.DefaultMaxIterations
	}
	return n.MaxIterations
}

func (n *SessionBootstrapNode) artifactPayload(state wfstate.State, input string) map[string]any {
	payload := map[string]any{
		"state_scope":    strings.TrimSpace(n.StateScope),
		"input":          input,
		"max_iterations": n.effectiveMaxIterations(),
		"request":        wfstate.CloneMap(state.Get(wfstate.StateKeyRequest)),
		"agent":          wfstate.CloneMap(state.Get(wfstate.StateKeyAgent)),
		"tool_policy":    wfstate.CloneMap(state.Get(wfstate.StateKeyToolPolicy)),
	}
	if messages, err := wfstate.SerializeMessages(state.Conversation(n.StateScope).Messages()); err == nil {
		payload["messages"] = messages
	}
	return payload
}

func mergeBootstrapMap(target wfstate.State, key string, values map[string]any) {
	if target == nil || key == "" {
		return
	}

	existing, _ := target[key].(map[string]any)
	if existing == nil {
		if typed, ok := target[key].(wfstate.State); ok {
			existing = typed
		}
	}
	if existing == nil {
		existing = map[string]any{}
	}
	mergeBootstrapValues(existing, values)
	target[key] = existing
}

func mergeBootstrapValues(target map[string]any, values map[string]any) {
	if target == nil {
		return
	}
	for key, value := range values {
		target[key] = wfstate.CloneValue(value)
	}
}
