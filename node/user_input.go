package node

import (
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

type UserInputNode struct {
	Base
	Prompt           string
	ValuePath        state.Path
	PendingInputPath state.Path
}

func NewUserInputNode(options ...NodeOption) *UserInputNode {
	target := &UserInputNode{
		Base: NewBase(Spec{
			Name:        NodeTypeUserInput,
			Description: "Ensure a user-provided value is available at an explicitly bound state path.",
		}),
		Prompt: "Waiting for user input.",
	}
	applyNodeOptions(&target.Base, options)
	ApplyDefaultStatePaths(target)
	return target
}

func (n *UserInputNode) Validate() error {
	if n == nil {
		return fmt.Errorf("user input node is nil")
	}
	if err := n.Base.Validate(); err != nil {
		return err
	}
	if n.ValuePath.Empty() {
		return fmt.Errorf("user input node %q requires value path", n.ID())
	}
	if n.PendingInputPath.Empty() {
		return fmt.Errorf("user input node %q requires pending input path", n.ID())
	}
	return nil
}

func (n *UserInputNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return newGraphNodeSpec(n.Base, NodeTypeUserInput, map[string]any{
		"prompt": n.Prompt,
	}, map[string]state.Path{
		"value": n.ValuePath, "pending_input": n.PendingInputPath,
	})
}

func UserInputNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type: NodeTypeUserInput, Title: "User Input",
			Description: "Use an existing request value or pause until the user provides one.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"prompt": dsl.JSONSchema{"type": "string", "default": "Waiting for user input."},
				},
				"additionalProperties": false,
			},
			StatePorts: []dsl.StatePortDefinition{
				primitivePortWithDefault("value", "User-provided text made available to downstream nodes.", "string", dsl.StateAccessReadWrite, false, "shared.request.input"),
				primitivePort("pending_input", "User text supplied when resuming an interrupted run.", "string", dsl.StateAccessReadWrite, false),
			},
		},
		Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (Node, error) {
			spec := resolved.Spec
			valuePath, err := resolvedPath(resolved, "value")
			if err != nil {
				return nil, err
			}
			pendingInputPath, err := resolvedPath(resolved, "pending_input")
			if err != nil {
				return nil, err
			}
			target := NewUserInputNode(WithID(spec.ID))
			applyNodeMetadata(&target.Base, spec)
			if prompt := strings.TrimSpace(config.String(spec.Config, "prompt")); prompt != "" {
				target.Prompt = prompt
			}
			target.ValuePath = valuePath
			target.PendingInputPath = pendingInputPath
			if err := target.Validate(); err != nil {
				return nil, err
			}
			return target, nil
		},
	}
}

func (n *UserInputNode) Execute(_ core.Context, access *state.Access) error {
	if pending, ok, err := n.consumePendingInput(access); err != nil {
		return err
	} else if ok {
		return access.SetAny(n.ValuePath, pending)
	}
	if raw, exists := access.ReadAny(n.ValuePath); exists {
		value, ok := raw.(string)
		if !ok {
			return fmt.Errorf("state path %q must be string, got %T", n.ValuePath.String(), raw)
		}
		if strings.TrimSpace(value) != "" {
			return nil
		}
	}
	return &core.NodeInterrupt{NodeID: n.ID(), Value: n.Prompt}
}

func (n *UserInputNode) consumePendingInput(access *state.Access) (string, bool, error) {
	raw, exists := access.ReadAny(n.PendingInputPath)
	if !exists {
		return "", false, nil
	}
	if err := access.Delete(n.PendingInputPath); err != nil {
		return "", false, err
	}
	if raw == nil {
		return "", false, nil
	}
	text, ok := raw.(string)
	if !ok {
		return "", false, fmt.Errorf("state path %q must be string, got %T", n.PendingInputPath.String(), raw)
	}
	text = strings.TrimSpace(text)
	return text, text != "", nil
}
