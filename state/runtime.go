package state

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
)

type BreakpointHit struct {
	BreakpointID string    `json:"breakpoint_id"`
	NodeID       string    `json:"node_id"`
	Stage        string    `json:"stage"`
	HitAt        time.Time `json:"hit_at"`
}

type Codec interface {
	Name() string
	Version() string
	Encode(snapshot Snapshot) ([]byte, error)
	Decode(data []byte) (Snapshot, error)
	Diff(before, after Snapshot) ([]Change, error)
}

type RestoredStateSnapshot struct {
	Snapshot  Snapshot      `json:"snapshot"`
	Business  *State        `json:"business"`
	Runtime   RuntimeState  `json:"runtime"`
	Artifacts []ArtifactRef `json:"artifacts,omitempty"`
}

type RuntimeState struct {
	RunID           string         `json:"run_id,omitempty"`
	CurrentStepID   string         `json:"current_step_id,omitempty"`
	CurrentTaskID   string         `json:"current_task_id,omitempty"`
	CurrentNodeID   string         `json:"current_node_id,omitempty"`
	CurrentNodeIDs  []string       `json:"current_node_ids,omitempty"`
	CurrentStepIDs  []string       `json:"current_step_ids,omitempty"`
	NextNodeIDs     []string       `json:"next_node_ids,omitempty"`
	ParallelWaveID  string         `json:"parallel_wave_id,omitempty"`
	WaveID          string         `json:"wave_id,omitempty"`
	Status          string         `json:"status,omitempty"`
	RetryCount      int            `json:"retry_count,omitempty"`
	PauseRequested  bool           `json:"pause_requested,omitempty"`
	CancelRequested bool           `json:"cancel_requested,omitempty"`
	BreakpointHit   *BreakpointHit `json:"breakpoint_hit,omitempty"`
}

type ArtifactRef struct {
	ID           string    `json:"id"`
	RunID        string    `json:"run_id,omitempty"`
	StepID       string    `json:"step_id,omitempty"`
	NodeID       string    `json:"node_id,omitempty"`
	OperationKey string    `json:"operation_key,omitempty"`
	ParentRunID  string    `json:"parent_run_id,omitempty"`
	ParentStepID string    `json:"parent_step_id,omitempty"`
	ParentTaskID string    `json:"parent_task_id,omitempty"`
	RootRunID    string    `json:"root_run_id,omitempty"`
	RunPath      []string  `json:"run_path,omitempty"`
	Namespace    string    `json:"namespace,omitempty"`
	Type         string    `json:"type,omitempty"`
	MIMEType     string    `json:"mime_type,omitempty"`
	Location     string    `json:"location,omitempty"`
	CreatedAt    time.Time `json:"created_at,omitempty"`
}

const runtimeArtifactsKey = "artifacts"

func SnapshotFromStateWithRuntime(current *State, runtime RuntimeState, artifacts []ArtifactRef) (Snapshot, error) {
	snapshot, err := SnapshotFromState(current)
	if err != nil {
		return Snapshot{}, err
	}
	runtimeMap, err := runtimeStateToMap(runtime)
	if err != nil {
		return Snapshot{}, err
	}
	if len(artifacts) > 0 {
		runtimeMap[runtimeArtifactsKey] = CloneArtifactRefs(artifacts)
	}
	snapshot.Runtime = emptyMapToNil(runtimeMap)
	return snapshot, nil
}

func RestoreStateSnapshot(snapshot Snapshot) (RestoredStateSnapshot, error) {
	restored, err := FromSnapshot(snapshot)
	if err != nil {
		return RestoredStateSnapshot{}, err
	}
	runtimeState, artifacts, err := runtimeStateFromMap(snapshot.Runtime)
	if err != nil {
		return RestoredStateSnapshot{}, err
	}
	return RestoredStateSnapshot{
		Snapshot:  snapshot,
		Business:  restored,
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
	return MergeResumeInput(base, input)
}

func SummaryFields(current *State) []zap.Field {
	return []zap.Field{
		zap.Int("state_keys", CountKeys(current)),
		zap.Int("state_scopes", CountScopes(current)),
	}
}

func CountKeys(current *State) int {
	if current == nil {
		return 0
	}
	shared, _ := current.root[SectionShared].(map[string]any)
	return len(shared)
}

func CountScopes(current *State) int {
	if current == nil {
		return 0
	}
	scopes, _ := current.root[SectionScopes].(map[string]any)
	return len(scopes)
}

func CloneArtifactRefs(artifacts []ArtifactRef) []ArtifactRef {
	if len(artifacts) == 0 {
		return nil
	}
	cloned := make([]ArtifactRef, len(artifacts))
	copy(cloned, artifacts)
	for index := range cloned {
		cloned[index].RunPath = append([]string(nil), artifacts[index].RunPath...)
	}
	return cloned
}

func ReadPath(current *State, path string) (any, bool) {
	parsed, err := ParsePath(path)
	if err != nil {
		return nil, false
	}
	return current.read(parsed)
}

func SetPath(current *State, path string, value any) error {
	parsed, err := ParsePath(path)
	if err != nil {
		return err
	}
	return current.set(parsed, value)
}

func DeletePath(current *State, path string) error {
	parsed, err := ParsePath(path)
	if err != nil {
		return err
	}
	return current.delete(parsed)
}

func MergePath(current *State, path string, value map[string]any) error {
	parsed, err := ParsePath(path)
	if err != nil {
		return err
	}
	return current.merge(parsed, value)
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
