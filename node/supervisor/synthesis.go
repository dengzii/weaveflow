package supervisor

import (
	"encoding/json"
	"fmt"
	"strings"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	supervisorcap "github.com/dengzii/weaveflow/capability/supervisor"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/registry"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"

	"github.com/dengzii/weaveflow/llms"
)

const defaultSupervisorSynthesisSystemPrompt = `You synthesize the final user-facing answer for a supervised team.
Use the completed worker results as evidence. Resolve overlaps or conflicts, preserve important caveats, and answer the objective directly.
Do not mention internal routing, worker ids, or the supervisor process unless the user explicitly asks.`

type SynthesisNode struct {
	core.NodeBase
	ModelID        string
	SystemPrompt   string
	SupervisorPath state.Path
	ResultPath     state.Path
}

func NewSynthesisNode(options ...core.NodeOption) *SynthesisNode {
	target := &SynthesisNode{
		NodeBase: core.NewNodeBase(core.NodeSpec{
			Name:        NodeTypeSupervisorSynthesis,
			Description: "Synthesize the final answer from the objective and completed supervisor delegations.",
		}),
		SystemPrompt: defaultSupervisorSynthesisSystemPrompt,
	}
	applyNodeOptions(&target.NodeBase, options)
	ApplyDefaultStatePaths(target)
	return target
}

func (n *SynthesisNode) Validate() error {
	if n == nil {
		return fmt.Errorf("supervisor synthesis node is nil")
	}
	if err := n.NodeBase.Validate(); err != nil {
		return err
	}
	if n.SupervisorPath.Empty() || n.ResultPath.Empty() {
		return fmt.Errorf("supervisor synthesis node %q requires supervisor and result paths", n.ID())
	}
	return nil
}

func (n *SynthesisNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return newGraphNodeSpec(n.NodeBase, NodeTypeSupervisorSynthesis, map[string]any{
		"model_id": n.ModelID, "system_prompt": n.SystemPrompt,
	}, map[string]state.Path{"supervisor": n.SupervisorPath, "result": n.ResultPath})
}

func SynthesisNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        NodeTypeSupervisorSynthesis,
			Title:       "Supervisor Synthesis",
			Description: "Generate the final user-facing answer from the supervisor objective and worker result history.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"model_id": dsl.JSONSchema{"type": "string", "title": "Model ID"},
					"system_prompt": dsl.JSONSchema{
						"type": "string", "title": "System Prompt", "x-control": "textarea", "default": defaultSupervisorSynthesisSystemPrompt,
					},
				},
				"additionalProperties": false,
			},
		},
		StatePorts: []dsl.StatePortDefinition{
			capabilityPort("supervisor", "Objective, history, and final status.", supervisorcap.CapabilityID, true,
				capabilityField(supervisorcap.FieldObjective, dsl.StateAccessRead),
				capabilityField(supervisorcap.FieldRoute, dsl.StateAccessWrite),
				capabilityField(supervisorcap.FieldTask, dsl.StateAccessWrite),
				capabilityField(supervisorcap.FieldStatus, dsl.StateAccessWrite),
				capabilityField(supervisorcap.FieldTurnCount, dsl.StateAccessRead),
				capabilityField(supervisorcap.FieldHistory, dsl.StateAccessRead)),
			primitivePort("result", "Final synthesized answer.", "string", dsl.StateAccessWrite, true),
		},
		Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (core.Node, error) {
			spec := resolved.Spec
			supervisorPath, err := resolvedPath(resolved, "supervisor")
			if err != nil {
				return nil, err
			}
			resultPath, err := resolvedPath(resolved, "result")
			if err != nil {
				return nil, err
			}
			target := NewSynthesisNode(core.WithID(spec.ID))
			applyNodeMetadata(&target.NodeBase, spec)
			target.ModelID = config.String(spec.Config, "model_id")
			if prompt := config.String(spec.Config, "system_prompt"); strings.TrimSpace(prompt) != "" {
				target.SystemPrompt = prompt
			}
			target.SupervisorPath = supervisorPath
			target.ResultPath = resultPath
			return target, nil
		},
	}
}

func (n *SynthesisNode) Execute(ctx core.Context, access *state.Access) (core.NodeResult, error) {
	return core.NodeResult{}, n.execute(ctx, access)
}

func (n *SynthesisNode) execute(ctx core.Context, access *state.Access) error {
	model := ctx.Model(n.ModelID)
	if model == nil {
		return fmt.Errorf("supervisor synthesis node: model %q not available", effectiveModelID(n.ModelID))
	}
	supervisorView, err := supervisorcap.Bind(access, n.SupervisorPath)
	if err != nil {
		return err
	}
	current := supervisorView.Value()
	objective := stringValue(current, SupervisorFieldObjective)
	if objective == "" {
		return fmt.Errorf("supervisor synthesis node %q requires an objective", n.ID())
	}
	history := historyFromValue(current[SupervisorFieldHistory])
	historyJSON, _ := json.MarshalIndent(history, "", "  ")
	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, n.effectiveSystemPrompt()),
		llms.TextParts(llms.ChatMessageTypeHuman, fmt.Sprintf("Objective:\n%s\n\nCompleted worker results:\n%s", objective, historyJSON)),
	}
	if serialized, serializeErr := conversationcap.SerializeMessages(messages); serializeErr == nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "supervisor.synthesis.prompt", map[string]any{"messages": serialized})
	}
	response, err := core.GenerateModel(ctx, model, llms.ModelRequest{
		ModelID:  effectiveModelID(n.ModelID),
		Mode:     llms.ModelModeChat,
		Messages: messages,
		Thinking: llms.ThinkingModeHigh,
	})
	if err != nil {
		return err
	}
	if response == nil || len(response.Choices) == 0 || response.Choices[0] == nil {
		return fmt.Errorf("supervisor synthesis node: llm returned no choices")
	}
	answer := strings.TrimSpace(response.Choices[0].Content)
	if answer == "" {
		return fmt.Errorf("supervisor synthesis node: llm returned an empty answer")
	}
	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "supervisor.synthesis.response", map[string]any{"content": answer})
	if err := state.Replace(access, state.NewRef[string](n.ResultPath), answer); err != nil {
		return err
	}
	if err := supervisorView.Merge(map[string]any{
		SupervisorFieldStatus: SupervisorStatusDone,
		SupervisorFieldRoute:  SupervisorRouteFinish,
		SupervisorFieldTask:   "",
	}); err != nil {
		return err
	}
	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, map[string]any{
		"event": "supervisor.synthesized", "turn_count": intValue(current, SupervisorFieldTurnCount), "answer": answer,
	})
	return nil
}

func (n *SynthesisNode) effectiveSystemPrompt() string {
	if n == nil || strings.TrimSpace(n.SystemPrompt) == "" {
		return defaultSupervisorSynthesisSystemPrompt
	}
	return strings.TrimSpace(n.SystemPrompt)
}
