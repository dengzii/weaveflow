package node

import (
	"errors"
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/registry"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"

	"github.com/dengzii/weaveflow/llms"
)

const defaultTextGenerationTemperature = 1.0

type TextGenerationNode struct {
	Base
	ModelID         string
	MaxTokens       int
	Temperature     float64
	StopWords       []string
	ReasoningEffort string
	PromptPath      state.Path
	OutputPath      state.Path
}

func NewTextGenerationNode(options ...NodeOption) *TextGenerationNode {
	node := &TextGenerationNode{
		Base: NewBase(Spec{
			Name:        NodeTypeTextGeneration,
			Description: "Generate text from a raw prompt without conversation messages.",
		}),
		Temperature:     defaultTextGenerationTemperature,
		ReasoningEffort: defaultReasoningEffort,
	}
	applyNodeOptions(&node.Base, options)
	ApplyDefaultStatePaths(node)
	return node
}

func (n *TextGenerationNode) Validate() error {
	if n == nil {
		return fmt.Errorf("text generation node is nil")
	}
	if err := n.Base.Validate(); err != nil {
		return err
	}
	if n.PromptPath.Empty() || n.OutputPath.Empty() {
		return fmt.Errorf("text generation node %q requires prompt and output paths", n.ID())
	}
	if n.MaxTokens < 0 {
		return fmt.Errorf("text generation node %q max_tokens must not be negative", n.ID())
	}
	if n.Temperature < 0 || n.Temperature > 2 {
		return fmt.Errorf("text generation node %q temperature must be between 0 and 2", n.ID())
	}
	if !isReasoningEffort(n.effectiveReasoningEffort()) {
		return fmt.Errorf("text generation node %q reasoning_effort must be one of %s", n.ID(), reasoningEffortOptions)
	}
	return nil
}

func (n *TextGenerationNode) GraphNodeSpec() dsl.GraphNodeSpec {
	conf := map[string]any{
		"temperature":      n.Temperature,
		"stop":             n.StopWords,
		"reasoning_effort": n.effectiveReasoningEffort(),
	}
	if strings.TrimSpace(n.ModelID) != "" {
		conf["model_id"] = n.ModelID
	}
	if n.MaxTokens > 0 {
		conf["max_tokens"] = n.MaxTokens
	}
	return newGraphNodeSpec(n.Base, NodeTypeTextGeneration, conf, map[string]state.Path{
		"prompt": n.PromptPath,
		"output": n.OutputPath,
	})
}

func TextGenerationNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        NodeTypeTextGeneration,
			Title:       "Text Generation",
			Description: "Generate text from a raw prompt without conversation messages.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"model_id": dsl.JSONSchema{"type": "string", "title": "Model ID"},
					"max_tokens": dsl.JSONSchema{
						"type": "integer", "title": "Max Tokens", "minimum": 1,
					},
					"temperature": dsl.JSONSchema{
						"type": "number", "title": "Temperature", "minimum": 0, "maximum": 2, "default": defaultTextGenerationTemperature,
					},
					"stop": dsl.JSONSchema{
						"type": "array", "title": "Stop Sequences", "items": dsl.JSONSchema{"type": "string"},
					},
					"reasoning_effort": reasoningEffortSchema(),
				},
				"additionalProperties": false,
			},
		},
		StatePorts: []dsl.StatePortDefinition{
			primitivePortWithDefault("prompt", "Raw text prompt supplied to the text generation model.", "string", dsl.StateAccessRead, true, "shared.text_generation.prompt"),
			primitivePortWithDefault("output", "Generated text.", "string", dsl.StateAccessWrite, true, "shared.text_generation.result"),
		},
		Build: func(ctx *registry.BuildContext, resolved registry.ResolvedNodeSpec) (Node, error) {
			_ = ctx
			spec := resolved.Spec
			promptPath, err := resolvedPath(resolved, "prompt")
			if err != nil {
				return nil, err
			}
			outputPath, err := resolvedPath(resolved, "output")
			if err != nil {
				return nil, err
			}

			target := NewTextGenerationNode(WithID(spec.ID))
			applyNodeMetadata(&target.Base, spec)
			target.ModelID = config.String(spec.Config, "model_id")
			target.StopWords = config.StringSlice(spec.Config, "stop")
			if reasoningEffort := strings.TrimSpace(config.String(spec.Config, "reasoning_effort")); reasoningEffort != "" {
				if !isReasoningEffort(reasoningEffort) {
					return nil, fmt.Errorf("build text generation node %q: reasoning_effort must be one of %s", spec.ID, reasoningEffortOptions)
				}
				target.ReasoningEffort = reasoningEffort
			}
			target.PromptPath = promptPath
			target.OutputPath = outputPath
			if value, ok := config.Int(spec.Config, "max_tokens"); ok {
				if value <= 0 {
					return nil, fmt.Errorf("build text generation node %q: max_tokens must be greater than 0", spec.ID)
				}
				target.MaxTokens = value
			}
			if value, ok := config.Float(spec.Config, "temperature"); ok {
				if value < 0 || value > 2 {
					return nil, fmt.Errorf("build text generation node %q: temperature must be between 0 and 2", spec.ID)
				}
				target.Temperature = value
			}
			return target, nil
		},
	}
}

func (n *TextGenerationNode) Execute(ctx core.Context, access *state.Access) (core.NodeResult, error) {
	return core.NodeResult{}, n.execute(ctx, access)
}

func (n *TextGenerationNode) execute(ctx core.Context, access *state.Access) error {
	model := ctx.Model(n.ModelID)
	if model == nil {
		return fmt.Errorf("text generation node: model %q not available", effectiveModelID(n.ModelID))
	}
	prompt, err := state.Get(access, state.NewRef[string](n.PromptPath).Required())
	if err != nil {
		return fmt.Errorf("text generation node %q prompt: %w", n.ID(), err)
	}
	if strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("text generation node %q prompt is empty", n.ID())
	}

	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "text_generation.prompt", map[string]any{
		"prompt":      prompt,
		"prompt_path": n.PromptPath.String(),
	})
	temperature := n.Temperature
	response, err := core.GenerateModel(ctx, model, llms.ModelRequest{
		ModelID:     effectiveModelID(n.ModelID),
		Mode:        llms.ModelModeCompletion,
		Prompt:      prompt,
		MaxTokens:   n.MaxTokens,
		Temperature: &temperature,
		StopWords:   append([]string(nil), n.StopWords...),
		Thinking:    llms.ThinkingMode(n.effectiveReasoningEffort()),
	})
	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "text_generation.error", map[string]any{"error": err.Error()})
		return err
	}
	if response == nil || len(response.Choices) == 0 || response.Choices[0] == nil {
		err := errors.New("text generation returned no choices")
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "text_generation.error", map[string]any{"error": err.Error()})
		return err
	}
	if payload := buildLLMResponseArtifact(response); len(payload.Choices) > 0 {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "text_generation.response", payload)
	}
	return state.Replace(access, state.NewRef[string](n.OutputPath), response.Choices[0].Content)
}

func (n *TextGenerationNode) effectiveReasoningEffort() string {
	if n == nil || strings.TrimSpace(n.ReasoningEffort) == "" {
		return defaultReasoningEffort
	}
	return strings.TrimSpace(n.ReasoningEffort)
}

func (n *TextGenerationNode) Contract() state.Contract {
	if n == nil {
		return state.Contract{}
	}
	return state.NewContract(
		state.NewRef[string](n.PromptPath).Required().ReadField(),
		state.NewRef[string](n.OutputPath).WriteField(),
	)
}
