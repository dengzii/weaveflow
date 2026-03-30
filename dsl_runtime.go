package falcon

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	GraphInstanceConfigVersion = "1.0"
	RunRequestVersion          = "1.0"
)

// SecretRef points to a secret value stored outside GraphDefinition.
type SecretRef struct {
	Source   string         `json:"source"`
	Ref      string         `json:"ref"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

func (r SecretRef) Validate() error {
	if strings.TrimSpace(r.Source) == "" {
		return fmt.Errorf("secret ref source is required")
	}
	if strings.TrimSpace(r.Ref) == "" {
		return fmt.Errorf("secret ref ref is required")
	}
	return nil
}

// GraphNodeInstanceConfig holds instance-bound values for a single node.
type GraphNodeInstanceConfig struct {
	Config   map[string]any       `json:"config,omitempty"`
	Secrets  map[string]SecretRef `json:"secrets,omitempty"`
	Disabled bool                 `json:"disabled,omitempty"`
	Metadata map[string]any       `json:"metadata,omitempty"`
}

func (c GraphNodeInstanceConfig) Validate(nodeID string) error {
	for key, ref := range c.Secrets {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("node %q secret key is required", nodeID)
		}
		if err := ref.Validate(); err != nil {
			return fmt.Errorf("node %q secret %q: %w", nodeID, key, err)
		}
	}
	return nil
}

// GraphInstanceConfig binds a graph definition to one runnable local instance.
type GraphInstanceConfig struct {
	Version      string                             `json:"version,omitempty"`
	ID           string                             `json:"id"`
	GraphRef     string                             `json:"graph_ref"`
	GraphVersion string                             `json:"graph_version,omitempty"`
	Name         string                             `json:"name,omitempty"`
	Description  string                             `json:"description,omitempty"`
	NodeConfigs  map[string]GraphNodeInstanceConfig `json:"node_configs,omitempty"`
	Secrets      map[string]SecretRef               `json:"secrets,omitempty"`
	Memory       map[string]any                     `json:"memory,omitempty"`
	Metadata     map[string]any                     `json:"metadata,omitempty"`
}

func normalizeGraphInstanceConfig(cfg GraphInstanceConfig) GraphInstanceConfig {
	if strings.TrimSpace(cfg.Version) == "" {
		cfg.Version = GraphInstanceConfigVersion
	}
	cfg.ID = strings.TrimSpace(cfg.ID)
	cfg.GraphRef = strings.TrimSpace(cfg.GraphRef)
	cfg.GraphVersion = strings.TrimSpace(cfg.GraphVersion)
	cfg.Name = strings.TrimSpace(cfg.Name)
	cfg.Description = strings.TrimSpace(cfg.Description)
	return cfg
}

func (c GraphInstanceConfig) Validate() error {
	c = normalizeGraphInstanceConfig(c)
	if c.ID == "" {
		return fmt.Errorf("graph instance id is required")
	}
	if c.GraphRef == "" {
		return fmt.Errorf("graph instance graph_ref is required")
	}
	for nodeID, nodeConfig := range c.NodeConfigs {
		trimmed := strings.TrimSpace(nodeID)
		if trimmed == "" {
			return fmt.Errorf("graph instance node config key is required")
		}
		if err := nodeConfig.Validate(trimmed); err != nil {
			return err
		}
	}
	for key, ref := range c.Secrets {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("graph instance secret key is required")
		}
		if err := ref.Validate(); err != nil {
			return fmt.Errorf("graph instance secret %q: %w", key, err)
		}
	}
	return nil
}

func SerializeGraphInstanceConfig(cfg GraphInstanceConfig) ([]byte, error) {
	cfg = normalizeGraphInstanceConfig(cfg)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return json.MarshalIndent(cfg, "", "  ")
}

func DeserializeGraphInstanceConfig(data []byte) (GraphInstanceConfig, error) {
	var cfg GraphInstanceConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return GraphInstanceConfig{}, err
	}
	cfg = normalizeGraphInstanceConfig(cfg)
	return cfg, cfg.Validate()
}

// DebugBreakpoint is the user-facing breakpoint shape used by RunRequest.
type DebugBreakpoint struct {
	ID      string `json:"id,omitempty"`
	NodeID  string `json:"node_id"`
	Stage   string `json:"stage,omitempty"`
	Enabled *bool  `json:"enabled,omitempty"`
}

func normalizeDebugBreakpoint(bp DebugBreakpoint) DebugBreakpoint {
	bp.ID = strings.TrimSpace(bp.ID)
	bp.NodeID = strings.TrimSpace(bp.NodeID)
	bp.Stage = strings.TrimSpace(bp.Stage)
	if bp.Stage == "" {
		bp.Stage = string(CheckpointBeforeNode)
	}
	return bp
}

func (b DebugBreakpoint) Validate() error {
	b = normalizeDebugBreakpoint(b)
	if b.NodeID == "" {
		return fmt.Errorf("breakpoint node_id is required")
	}
	if !isValidCheckpointStage(b.Stage) {
		return fmt.Errorf("breakpoint stage %q is invalid", b.Stage)
	}
	return nil
}

func (b DebugBreakpoint) RuntimeBreakpoint() Breakpoint {
	b = normalizeDebugBreakpoint(b)
	id := b.ID
	if id == "" {
		id = fmt.Sprintf("%s:%s", b.Stage, b.NodeID)
	}
	enabled := true
	if b.Enabled != nil {
		enabled = *b.Enabled
	}
	return Breakpoint{
		ID:      id,
		NodeID:  b.NodeID,
		Stage:   b.Stage,
		Enabled: enabled,
	}
}

// RunDebugOptions controls one execution's debug behavior.
type RunDebugOptions struct {
	StepMode         bool              `json:"step_mode,omitempty"`
	Breakpoints      []DebugBreakpoint `json:"breakpoints,omitempty"`
	PauseBefore      []string          `json:"pause_before,omitempty"`
	PauseAfter       []string          `json:"pause_after,omitempty"`
	IncludeState     bool              `json:"include_state,omitempty"`
	IncludeStateDiff bool              `json:"include_state_diff,omitempty"`
	IncludeArtifacts bool              `json:"include_artifacts,omitempty"`
	IncludeSnapshots bool              `json:"include_snapshots,omitempty"`
}

func (o RunDebugOptions) Validate() error {
	for _, bp := range o.Breakpoints {
		if err := bp.Validate(); err != nil {
			return err
		}
	}
	for _, nodeID := range o.PauseBefore {
		if strings.TrimSpace(nodeID) == "" {
			return fmt.Errorf("pause_before contains an empty node id")
		}
	}
	for _, nodeID := range o.PauseAfter {
		if strings.TrimSpace(nodeID) == "" {
			return fmt.Errorf("pause_after contains an empty node id")
		}
	}
	return nil
}

func (o RunDebugOptions) EffectiveBreakpoints() []Breakpoint {
	seen := map[string]struct{}{}
	items := make([]Breakpoint, 0, len(o.Breakpoints)+len(o.PauseBefore)+len(o.PauseAfter))
	appendBreakpoint := func(bp Breakpoint) {
		key := bp.NodeID + "|" + bp.Stage
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		items = append(items, bp)
	}
	for _, bp := range o.Breakpoints {
		appendBreakpoint(bp.RuntimeBreakpoint())
	}
	for _, nodeID := range o.PauseBefore {
		trimmed := strings.TrimSpace(nodeID)
		if trimmed == "" {
			continue
		}
		appendBreakpoint(Breakpoint{
			ID:      fmt.Sprintf("%s:%s", CheckpointBeforeNode, trimmed),
			NodeID:  trimmed,
			Stage:   string(CheckpointBeforeNode),
			Enabled: true,
		})
	}
	for _, nodeID := range o.PauseAfter {
		trimmed := strings.TrimSpace(nodeID)
		if trimmed == "" {
			continue
		}
		appendBreakpoint(Breakpoint{
			ID:      fmt.Sprintf("%s:%s", CheckpointAfterNode, trimmed),
			NodeID:  trimmed,
			Stage:   string(CheckpointAfterNode),
			Enabled: true,
		})
	}
	return items
}

// RunRequest describes a single execution against one graph instance.
type RunRequest struct {
	Version                string           `json:"version,omitempty"`
	InstanceID             string           `json:"instance_id"`
	Input                  State            `json:"input,omitempty"`
	Stream                 bool             `json:"stream,omitempty"`
	Debug                  *RunDebugOptions `json:"debug,omitempty"`
	ResumeFromRunID        string           `json:"resume_from_run_id,omitempty"`
	ResumeFromCheckpointID string           `json:"resume_from_checkpoint_id,omitempty"`
	Metadata               map[string]any   `json:"metadata,omitempty"`
}

func normalizeRunRequest(req RunRequest) RunRequest {
	if strings.TrimSpace(req.Version) == "" {
		req.Version = RunRequestVersion
	}
	req.InstanceID = strings.TrimSpace(req.InstanceID)
	req.ResumeFromRunID = strings.TrimSpace(req.ResumeFromRunID)
	req.ResumeFromCheckpointID = strings.TrimSpace(req.ResumeFromCheckpointID)
	return req
}

func (r RunRequest) Validate() error {
	r = normalizeRunRequest(r)
	if r.InstanceID == "" {
		return fmt.Errorf("run request instance_id is required")
	}
	if r.ResumeFromRunID != "" && r.ResumeFromCheckpointID != "" {
		return fmt.Errorf("run request can specify only one of resume_from_run_id or resume_from_checkpoint_id")
	}
	if (r.ResumeFromRunID != "" || r.ResumeFromCheckpointID != "") && len(r.Input) > 0 {
		return fmt.Errorf("run request input cannot be combined with resume fields")
	}
	if r.Debug != nil {
		if err := r.Debug.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func SerializeRunRequest(req RunRequest) ([]byte, error) {
	req = normalizeRunRequest(req)
	if err := req.Validate(); err != nil {
		return nil, err
	}
	return json.MarshalIndent(req, "", "  ")
}

func DeserializeRunRequest(data []byte) (RunRequest, error) {
	var req RunRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return RunRequest{}, err
	}
	req = normalizeRunRequest(req)
	return req, req.Validate()
}

func isValidCheckpointStage(stage string) bool {
	switch stage {
	case string(CheckpointBeforeNode), string(CheckpointAfterNode):
		return true
	default:
		return false
	}
}
