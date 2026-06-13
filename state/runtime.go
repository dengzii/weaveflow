package state

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dengzii/weaveflow/core"

	"github.com/tmc/langchaingo/llms"
	"go.uber.org/zap"
)

type BreakpointHit = core.BreakpointHit

type StateCodec interface {
	Name() string
	Version() string
	Encode(snapshot StateSnapshot) ([]byte, error)
	Decode(data []byte) (StateSnapshot, error)
	Diff(before, after StateSnapshot) ([]StateChange, error)
}

type RestoredStateSnapshot struct {
	Snapshot  StateSnapshot `json:"snapshot"`
	Business  *State        `json:"business"`
	Runtime   RuntimeState  `json:"runtime"`
	Artifacts []ArtifactRef `json:"artifacts,omitempty"`
}

type RuntimeState struct {
	RunID           string         `json:"run_id,omitempty"`
	CurrentStepID   string         `json:"current_step_id,omitempty"`
	CurrentNodeID   string         `json:"current_node_id,omitempty"`
	Status          string         `json:"status,omitempty"`
	RetryCount      int            `json:"retry_count,omitempty"`
	PauseRequested  bool           `json:"pause_requested,omitempty"`
	CancelRequested bool           `json:"cancel_requested,omitempty"`
	BreakpointHit   *BreakpointHit `json:"breakpoint_hit,omitempty"`
}

type ArtifactRef struct {
	ID        string    `json:"id"`
	RunID     string    `json:"run_id,omitempty"`
	StepID    string    `json:"step_id,omitempty"`
	NodeID    string    `json:"node_id,omitempty"`
	Type      string    `json:"type,omitempty"`
	MIMEType  string    `json:"mime_type,omitempty"`
	Location  string    `json:"location,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

const runtimeArtifactsKey = "artifacts"

const (
	runtimeConversationKey                 = "conversation"
	runtimeConversationFieldMessages       = "messages"
	runtimeConversationFieldFinalAnswer    = "final_answer"
	runtimeConversationFieldIterationCount = "iteration_count"
)

func SnapshotFromStateWithRuntime(state *State, runtime RuntimeState, artifacts []ArtifactRef) (StateSnapshot, error) {
	snapshot, err := SnapshotFromState(state)
	if err != nil {
		return StateSnapshot{}, err
	}
	runtimeMap, err := runtimeStateToMap(runtime)
	if err != nil {
		return StateSnapshot{}, err
	}
	if len(artifacts) > 0 {
		runtimeMap[runtimeArtifactsKey] = CloneArtifactRefs(artifacts)
	}
	snapshot.Runtime = emptyMapToNil(runtimeMap)
	return snapshot, nil
}

func RestoreStateSnapshot(snapshot StateSnapshot) (RestoredStateSnapshot, error) {
	state, err := StateFromSnapshot(snapshot)
	if err != nil {
		return RestoredStateSnapshot{}, err
	}
	runtimeState, artifacts, err := runtimeStateFromMap(snapshot.Runtime)
	if err != nil {
		return RestoredStateSnapshot{}, err
	}
	return RestoredStateSnapshot{
		Snapshot:  snapshot,
		Business:  state,
		Runtime:   runtimeState,
		Artifacts: artifacts,
	}, nil
}

func MergeResumeInput(base *State, input *State) (*State, error) {
	if input == nil {
		if base == nil {
			return NewState(), nil
		}
		return base.Clone(), nil
	}
	merged := NewState()
	if base != nil {
		merged = base.Clone()
	}
	mergeMap(merged.root, input.Export())
	merged.ensureRootSections()
	return merged, nil
}

func PrepareContinuationState(base *State, input *State) (*State, error) {
	state := NewState()
	if base != nil {
		state = base.Clone()
	}
	resetConversationTurnState(state)
	return MergeResumeInput(state, input)
}

func SummaryFields(state *State) []zap.Field {
	return []zap.Field{
		zap.Int("state_keys", CountKeys(state)),
		zap.Int("state_scopes", CountScopes(state)),
		zap.Int("conversation_messages", CountConversationMessages(state)),
	}
}

func CountKeys(state *State) int {
	if state == nil {
		return 0
	}
	shared, _ := state.root[SectionShared].(map[string]any)
	return len(shared)
}

func CountScopes(state *State) int {
	if state == nil {
		return 0
	}
	scopes, _ := state.root[SectionScopes].(map[string]any)
	return len(scopes)
}

func CountConversationMessages(state *State) int {
	if state == nil {
		return 0
	}
	total := countMessagesAt(state, statePath(SectionShared, runtimeConversationKey))
	scopes, _ := state.root[SectionScopes].(map[string]any)
	for scopeName := range scopes {
		total += countMessagesAt(state, statePath(SectionScopes, scopeName, runtimeConversationKey))
	}
	return total
}

func CloneArtifactRefs(artifacts []ArtifactRef) []ArtifactRef {
	if len(artifacts) == 0 {
		return nil
	}
	cloned := make([]ArtifactRef, len(artifacts))
	copy(cloned, artifacts)
	return cloned
}

func ReadPath(state *State, path string) (any, bool) {
	parsed, err := ParsePath(path)
	if err != nil {
		return nil, false
	}
	return state.read(parsed)
}

func SetPath(state *State, path string, value any) error {
	parsed, err := ParsePath(path)
	if err != nil {
		return err
	}
	return state.set(parsed, value)
}

func MergePath(state *State, path string, value map[string]any) error {
	parsed, err := ParsePath(path)
	if err != nil {
		return err
	}
	return state.merge(parsed, value)
}

func ResolveStateValue(root any, segments []string) (any, bool) {
	current := root
	for _, segment := range segments {
		if strings.TrimSpace(segment) == "" {
			return nil, false
		}
		mapped, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = mapped[segment]
		if !ok {
			return nil, false
		}
	}
	return cloneValue(current), true
}

func SplitStatePath(path string) []string {
	parts := strings.Split(strings.TrimSpace(path), ".")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func runtimeStateToMap(runtime RuntimeState) (map[string]any, error) {
	raw, err := json.Marshal(runtime)
	if err != nil {
		return nil, err
	}
	values := map[string]any{}
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	return normalizeDecodedMap(values), nil
}

func runtimeStateFromMap(values map[string]any) (RuntimeState, []ArtifactRef, error) {
	values = cloneMap(values)
	artifacts, err := artifactRefsFromValue(values[runtimeArtifactsKey])
	if err != nil {
		return RuntimeState{}, nil, err
	}
	delete(values, runtimeArtifactsKey)
	raw, err := json.Marshal(values)
	if err != nil {
		return RuntimeState{}, nil, err
	}
	var runtime RuntimeState
	if err := json.Unmarshal(raw, &runtime); err != nil {
		return RuntimeState{}, nil, err
	}
	return runtime, artifacts, nil
}

func artifactRefsFromValue(value any) ([]ArtifactRef, error) {
	if value == nil {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var artifacts []ArtifactRef
	if err := json.Unmarshal(raw, &artifacts); err != nil {
		return nil, fmt.Errorf("decode runtime artifacts: %w", err)
	}
	return CloneArtifactRefs(artifacts), nil
}

func resetConversationTurnState(state *State) {
	if state == nil {
		return
	}
	deleteConversationTurn(state, Shared(runtimeConversationKey))
	scopes, _ := state.root[SectionScopes].(map[string]any)
	for scopeName := range scopes {
		deleteConversationTurn(state, Scope(scopeName, runtimeConversationKey))
	}
}

func deleteConversationTurn(state *State, path Path) {
	_ = state.delete(path.MustChild(runtimeConversationFieldFinalAnswer))
	_ = state.delete(path.MustChild(runtimeConversationFieldIterationCount))
}

func countMessagesAt(state *State, path string) int {
	parsed, err := ParsePath(path + "." + runtimeConversationFieldMessages)
	if err != nil {
		return 0
	}
	raw, ok := state.read(parsed)
	if !ok {
		return 0
	}
	switch typed := raw.(type) {
	case []llms.MessageContent:
		return len(typed)
	case []StateMessage:
		return len(typed)
	case []any:
		return len(typed)
	default:
		return 0
	}
}

func statePath(section string, segments ...string) string {
	if len(segments) == 0 {
		return section
	}
	return section + "." + strings.Join(segments, ".")
}
