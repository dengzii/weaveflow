package claude

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	basenode "github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/registry"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

const NodeType = "claude"

const (
	claudeProgressKind      = "claude_progress"
	claudeProgressStarted   = "claude.started"
	claudeProgressRunning   = "claude.progress"
	claudeProgressCompleted = "claude.completed"
	claudeProgressFailed    = "claude.failed"
)

var _ dsl.GraphNodeSpecProvider = (*Node)(nil)

type Node struct {
	core.NodeBase
	PromptPath state.Path
	OutputPath state.Path
}

type progressEvent struct {
	Kind       string  `json:"kind"`
	Event      string  `json:"event"`
	Provider   string  `json:"provider"`
	Model      string  `json:"model,omitempty"`
	SessionID  string  `json:"session_id,omitempty"`
	Status     string  `json:"status"`
	Channel    string  `json:"channel,omitempty"`
	Message    string  `json:"message,omitempty"`
	Usage      *Usage  `json:"usage,omitempty"`
	CostUSD    float64 `json:"cost_usd,omitempty"`
	NumTurns   int     `json:"num_turns,omitempty"`
	DurationMS int64   `json:"duration_ms,omitempty"`
}

func NewNode(options ...core.NodeOption) *Node {
	target := &Node{
		NodeBase: core.NewNodeBase(core.NodeSpec{
			Name:        "Claude Code",
			Description: "Run Claude Code against the server-controlled workspace and policy.",
		}),
	}
	core.ApplyNodeOptions(&target.NodeBase, options)
	target.ApplyDefaultStatePaths()
	return target
}

func (node *Node) Validate() error {
	if node == nil {
		return fmt.Errorf("Claude node is nil")
	}
	if err := node.NodeBase.Validate(); err != nil {
		return err
	}
	if node.PromptPath.Empty() || node.OutputPath.Empty() {
		return fmt.Errorf("Claude node %q requires prompt and output paths", node.ID())
	}
	return nil
}

func (node *Node) ApplyDefaultStatePaths() {
	if node == nil {
		return
	}
	if node.PromptPath.Empty() {
		node.PromptPath = state.Shared("claude", "prompt")
	}
	if node.OutputPath.Empty() {
		node.OutputPath = state.Shared("claude", "result")
	}
}

func (node *Node) GraphNodeSpec() dsl.GraphNodeSpec {
	if node == nil {
		return dsl.GraphNodeSpec{}
	}
	return basenode.NewGraphNodeSpec(node.NodeBase, NodeType, map[string]any{}, map[string]state.Path{
		"prompt": node.PromptPath,
		"output": node.OutputPath,
	})
}

func NodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        NodeType,
			Title:       "Claude Code",
			Description: "Run Claude Code against the server-controlled workspace and policy.",
			ConfigSchema: dsl.JSONSchema{
				"type":                 "object",
				"properties":           dsl.JSONSchema{},
				"additionalProperties": false,
			},
		},
		StatePorts: []dsl.StatePortDefinition{
			basenode.PrimitivePortWithDefault("prompt", "Instructions sent to Claude Code through stdin.", "string", dsl.StateAccessRead, true, "shared.claude.prompt"),
			basenode.PrimitivePortWithDefault("output", "Final Claude Code result.", "string", dsl.StateAccessWrite, true, "shared.claude.result"),
		},
		Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (core.Node, error) {
			spec := resolved.Spec
			promptPath, err := basenode.ResolvedPath(resolved, "prompt")
			if err != nil {
				return nil, err
			}
			outputPath, err := basenode.ResolvedPath(resolved, "output")
			if err != nil {
				return nil, err
			}
			target := NewNode(core.WithID(spec.ID))
			basenode.ApplyNodeMetadata(&target.NodeBase, spec)
			target.PromptPath = promptPath
			target.OutputPath = outputPath
			return target, nil
		},
	}
}

func (node *Node) Execute(ctx core.Context, access *state.Access) (core.NodeResult, error) {
	return core.NodeResult{}, node.execute(ctx, access)
}

func (node *Node) execute(ctx core.Context, access *state.Access) error {
	prompt, err := state.Get(access, state.NewRef[string](node.PromptPath).Required())
	if err != nil {
		return fmt.Errorf("Claude node %q prompt: %w", node.ID(), err)
	}
	if strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("Claude node %q prompt is empty", node.ID())
	}
	runner := RunnerFromContext(ctx)
	if runner == nil {
		return fmt.Errorf("Claude node %q runner is not configured", node.ID())
	}
	if err := node.publishProgress(ctx, progressEvent{
		Event:   claudeProgressStarted,
		Status:  "started",
		Message: "Claude Code execution started",
	}); err != nil {
		return fmt.Errorf("Claude node %q publish start progress: %w", node.ID(), err)
	}

	result, runErr := runner.Run(ctx, RunRequest{
		Prompt: prompt,
		OnChunk: func(chunk Chunk) error {
			return node.publishProgress(ctx, progressEvent{
				Model:     chunk.Model,
				Event:     claudeProgressRunning,
				SessionID: chunk.SessionID,
				Status:    "running",
				Channel:   chunk.Channel,
				Message:   chunk.Text,
			})
		},
	})
	node.saveArtifacts(ctx, prompt, result, runErr)
	if runErr != nil {
		_ = node.publishProgress(ctx, progressEvent{
			Model:      result.Model,
			Event:      claudeProgressFailed,
			SessionID:  result.SessionID,
			Status:     "failed",
			Message:    runErr.Error(),
			DurationMS: result.Duration.Milliseconds(),
		})
		return fmt.Errorf("Claude node %q: %w", node.ID(), runErr)
	}
	if err := state.Replace(access, state.NewRef[string](node.OutputPath), result.Output); err != nil {
		_ = node.publishProgress(ctx, progressEvent{
			Model:      result.Model,
			Event:      claudeProgressFailed,
			SessionID:  result.SessionID,
			Status:     "failed",
			Message:    err.Error(),
			DurationMS: result.Duration.Milliseconds(),
		})
		return err
	}
	usage := result.Usage
	if err := node.publishProgress(ctx, progressEvent{
		Model:      result.Model,
		Event:      claudeProgressCompleted,
		SessionID:  result.SessionID,
		Status:     "completed",
		Message:    "Claude Code execution completed",
		Usage:      &usage,
		CostUSD:    result.CostUSD,
		NumTurns:   result.NumTurns,
		DurationMS: result.Duration.Milliseconds(),
	}); err != nil {
		return fmt.Errorf("Claude node %q publish completion progress: %w", node.ID(), err)
	}
	return nil
}

func (node *Node) publishProgress(ctx core.Context, event progressEvent) error {
	event.Kind = claudeProgressKind
	event.Provider = "claude"
	return fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, event)
}

func (node *Node) saveArtifacts(ctx core.Context, prompt string, result RunResult, runErr error) {
	promptHash := sha256.Sum256([]byte(prompt))
	summary := map[string]any{
		"provider":      "claude",
		"model":         result.Model,
		"session_id":    result.SessionID,
		"usage":         result.Usage,
		"cost_usd":      result.CostUSD,
		"num_turns":     result.NumTurns,
		"duration_ms":   result.Duration.Milliseconds(),
		"exit_code":     result.ExitCode,
		"truncated":     result.Truncated,
		"prompt_bytes":  len([]byte(prompt)),
		"prompt_sha256": fmt.Sprintf("%x", promptHash[:]),
	}
	if runErr != nil {
		summary["error"] = runErr.Error()
	}
	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "claude.summary", summary)
	if result.Stderr != "" {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "claude.stderr", map[string]any{"text": result.Stderr})
	}
	if len(result.Events) > 0 {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "claude.events", result.Events)
	}
	if result.Output != "" {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "claude.output", map[string]any{"text": result.Output})
	}
}

func (node *Node) Contract() state.Contract {
	if node == nil {
		return state.Contract{}
	}
	return state.NewContract(
		state.NewRef[string](node.PromptPath).Required().ReadField(),
		state.NewRef[string](node.OutputPath).WriteField(),
	)
}
