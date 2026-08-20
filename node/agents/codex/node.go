package codex

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	basenode "github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/registry"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

const NodeType = "codex"

const (
	codexProgressKind      = "codex_progress"
	codexProgressStarted   = "codex.started"
	codexProgressRunning   = "codex.progress"
	codexProgressCompleted = "codex.completed"
	codexProgressFailed    = "codex.failed"
)

var _ dsl.GraphNodeSpecProvider = (*Node)(nil)

type Node struct {
	core.NodeBase
	ModelID    string
	PromptPath state.Path
	OutputPath state.Path
}

type progressEvent struct {
	Kind       string `json:"kind"`
	Event      string `json:"event"`
	Provider   string `json:"provider"`
	ModelID    string `json:"model_id"`
	ThreadID   string `json:"thread_id,omitempty"`
	Status     string `json:"status"`
	Channel    string `json:"channel,omitempty"`
	Message    string `json:"message,omitempty"`
	Usage      *Usage `json:"usage,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

func NewNode(options ...core.NodeOption) *Node {
	target := &Node{
		NodeBase: core.NewNodeBase(core.NodeSpec{
			Name:        "Codex",
			Description: "Run Codex against the active Graph Settings model and workspace.",
		}),
	}
	core.ApplyNodeOptions(&target.NodeBase, options)
	target.ApplyDefaultStatePaths()
	return target
}

func (node *Node) Validate() error {
	if node == nil {
		return fmt.Errorf("codex node is nil")
	}
	if err := node.NodeBase.Validate(); err != nil {
		return err
	}
	if node.PromptPath.Empty() || node.OutputPath.Empty() {
		return fmt.Errorf("codex node %q requires prompt and output paths", node.ID())
	}
	return nil
}

func (node *Node) ApplyDefaultStatePaths() {
	if node == nil {
		return
	}
	if node.PromptPath.Empty() {
		node.PromptPath = state.Shared("codex", "prompt")
	}
	if node.OutputPath.Empty() {
		node.OutputPath = state.Shared("codex", "result")
	}
}

func (node *Node) GraphNodeSpec() dsl.GraphNodeSpec {
	if node == nil {
		return dsl.GraphNodeSpec{}
	}
	nodeConfig := map[string]any{}
	if strings.TrimSpace(node.ModelID) != "" {
		nodeConfig["model_id"] = node.ModelID
	}
	return basenode.NewGraphNodeSpec(node.NodeBase, NodeType, nodeConfig, map[string]state.Path{
		"prompt": node.PromptPath,
		"output": node.OutputPath,
	})
}

func NodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        NodeType,
			Title:       "Codex",
			Description: "Run Codex against the active Graph Settings model and workspace.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"model_id": dsl.JSONSchema{
						"type":        "string",
						"title":       "Model ID",
						"description": "Model configured in Graph Settings; blank uses the default model.",
					},
				},
				"additionalProperties": false,
			},
		},
		StatePorts: []dsl.StatePortDefinition{
			basenode.PrimitivePortWithDefault("prompt", "Instructions sent to Codex through stdin.", "string", dsl.StateAccessRead, true, "shared.codex.prompt"),
			basenode.PrimitivePortWithDefault("output", "Final Codex agent message.", "string", dsl.StateAccessWrite, true, "shared.codex.result"),
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
			target.ModelID = strings.TrimSpace(config.String(spec.Config, "model_id"))
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
		return fmt.Errorf("codex node %q prompt: %w", node.ID(), err)
	}
	if strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("codex node %q prompt is empty", node.ID())
	}
	runner := RunnerFromContext(ctx)
	if runner == nil {
		return fmt.Errorf("codex node %q runner is not configured", node.ID())
	}
	if err := node.publishProgress(ctx, progressEvent{
		Event:   codexProgressStarted,
		Status:  "started",
		Message: "Codex execution started",
	}); err != nil {
		return fmt.Errorf("codex node %q publish start progress: %w", node.ID(), err)
	}

	result, runErr := runner.Run(ctx, RunRequest{
		ModelID: node.ModelID,
		Prompt:  prompt,
		OnChunk: func(chunk Chunk) error {
			return node.publishProgress(ctx, progressEvent{
				ModelID:  chunk.ModelID,
				Event:    codexProgressRunning,
				ThreadID: chunk.ThreadID,
				Status:   "running",
				Channel:  chunk.Channel,
				Message:  chunk.Text,
			})
		},
	})
	node.saveArtifacts(ctx, prompt, result, runErr)
	if runErr != nil {
		_ = node.publishProgress(ctx, progressEvent{
			ModelID:    result.ModelID,
			Event:      codexProgressFailed,
			ThreadID:   result.ThreadID,
			Status:     "failed",
			Message:    runErr.Error(),
			DurationMS: result.Duration.Milliseconds(),
		})
		return fmt.Errorf("codex node %q: %w", node.ID(), runErr)
	}
	if err := state.Replace(access, state.NewRef[string](node.OutputPath), result.Output); err != nil {
		_ = node.publishProgress(ctx, progressEvent{
			ModelID:    result.ModelID,
			Event:      codexProgressFailed,
			ThreadID:   result.ThreadID,
			Status:     "failed",
			Message:    err.Error(),
			DurationMS: result.Duration.Milliseconds(),
		})
		return err
	}
	usage := result.Usage
	if err := node.publishProgress(ctx, progressEvent{
		ModelID:    result.ModelID,
		Event:      codexProgressCompleted,
		ThreadID:   result.ThreadID,
		Status:     "completed",
		Message:    "Codex execution completed",
		Usage:      &usage,
		DurationMS: result.Duration.Milliseconds(),
	}); err != nil {
		return fmt.Errorf("codex node %q publish completion progress: %w", node.ID(), err)
	}
	return nil
}

func (node *Node) publishProgress(ctx core.Context, event progressEvent) error {
	event.Kind = codexProgressKind
	event.Provider = "codex"
	if strings.TrimSpace(event.ModelID) == "" {
		event.ModelID = effectiveCodexModelID(node.ModelID)
	}
	return fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, event)
}

func (node *Node) saveArtifacts(ctx core.Context, prompt string, result RunResult, runErr error) {
	promptHash := sha256.Sum256([]byte(prompt))
	modelID := strings.TrimSpace(result.ModelID)
	if modelID == "" {
		modelID = effectiveCodexModelID(node.ModelID)
	}
	summary := map[string]any{
		"provider":      "codex",
		"model_id":      modelID,
		"thread_id":     result.ThreadID,
		"usage":         result.Usage,
		"duration_ms":   result.Duration.Milliseconds(),
		"exit_code":     result.ExitCode,
		"truncated":     result.Truncated,
		"prompt_bytes":  len([]byte(prompt)),
		"prompt_sha256": fmt.Sprintf("%x", promptHash[:]),
	}
	if runErr != nil {
		summary["error"] = runErr.Error()
	}
	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "codex.summary", summary)
	if result.Stderr != "" {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "codex.stderr", map[string]any{"text": result.Stderr})
	}
	if len(result.Events) > 0 {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "codex.events", result.Events)
	}
	if result.Output != "" {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "codex.output", map[string]any{"text": result.Output})
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
